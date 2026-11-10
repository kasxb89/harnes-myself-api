/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'

import { PhoneLoginGrantsSection } from '../phone-login-grants-section'

const grant = {
  phone_e164: '+60123456789',
  issued_at: 1,
  expires_at: Math.floor(Date.now() / 1000) + 600,
  used_at: 0,
  revoked_at: 0,
  attempts: 0,
}

async function fixture(items = [grant]) {
  const i18n = createInstance()
  await i18n.init({
    lng: 'en',
    resources: { en: { translation: {} } },
    interpolation: { escapeValue: false },
  })
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/verify/methods') {
      return {
        data: {
          success: true,
          data: {
            scope: 'admin.user.phone_grant',
            methods: [{ method: 'password', available: true }],
            oauth_providers: [],
            password_encryption_enabled: false,
          },
        },
      }
    }
    return { data: { success: true, data: { items, total: items.length } } }
  })
  const post = vi.spyOn(api, 'post').mockImplementation(async (url, data) => {
    if (url === '/api/verify') {
      return {
        data: {
          success: true,
          data: {
            proof_token: 'test-step-up-proof',
            expires_at: grant.expires_at,
            scope: 'admin.user.phone_grant',
            method: 'password',
          },
        },
      }
    }
    const request = data as { action: string }
    return {
      data: {
        success: true,
        data: request.action === 'issue' ? { grant, code: '482961' } : null,
      },
    }
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={client}>
        <PhoneLoginGrantsSection />
      </QueryClientProvider>
    </I18nextProvider>
  )
  return { get, post, user: userEvent.setup() }
}

beforeEach(() => {
  vi.restoreAllMocks()
})

async function verify(user: ReturnType<typeof userEvent.setup>) {
  const dialog = await screen.findByRole('dialog')
  await user.type(
    within(dialog).getByLabelText('Password', { selector: 'input' }),
    'local-fixture-password'
  )
  await user.click(within(dialog).getByRole('button', { name: /^Verify$/ }))
}

describe('Overseas phone whitelist', () => {
  it('rejects domestic and wildcard numbers before issuance', async () => {
    const { user, post } = await fixture([])
    const input = screen.getByLabelText('International phone number')
    for (const value of ['+8613800138080', '+60*']) {
      await user.clear(input)
      await user.type(input, value)
      await user.click(
        screen.getByRole('button', { name: 'Generate private login code' })
      )
      expect(
        await screen.findByText(
          'Enter the full international number including its country code, excluding +86.'
        )
      ).toBeVisible()
    }
    expect(post).not.toHaveBeenCalled()
  })

  it('binds step-up to the number, shows code once and never writes browser storage', async () => {
    const { user, post } = await fixture([])
    const storage = vi.spyOn(Storage.prototype, 'setItem')
    await user.type(
      screen.getByLabelText('International phone number'),
      grant.phone_e164
    )
    await user.click(
      screen.getByRole('button', { name: 'Generate private login code' })
    )
    expect(post).not.toHaveBeenCalled()
    await verify(user)
    expect(await screen.findByText('482961')).toBeVisible()
    expect(post).toHaveBeenCalledWith(
      '/api/verify',
      expect.objectContaining({
        scope: 'admin.user.phone_grant',
        context: { phone_e164: grant.phone_e164, action: 'issue' },
      }),
      expect.anything()
    )
    expect(post).toHaveBeenCalledWith(
      '/api/option/phone_login_grants',
      { phone_e164: grant.phone_e164, action: 'issue' },
      expect.objectContaining({
        headers: { 'X-Security-Proof': 'test-step-up-proof' },
      })
    )
    expect(
      screen.getByRole('button', { name: 'Copy login code' })
    ).toBeVisible()
    expect(storage).not.toHaveBeenCalled()
    await user.click(screen.getByRole('button', { name: 'Dismiss' }))
    expect(screen.queryByText('482961')).not.toBeInTheDocument()
  })

  it('cancelling operator verification sends no grant request', async () => {
    const { user, post } = await fixture()
    await user.click(
      await screen.findByRole('button', { name: 'New login code' })
    )
    const dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }))
    expect(post).not.toHaveBeenCalled()
  })

  it('revoke requires confirmation and a new proof without affecting sessions', async () => {
    const { user, post } = await fixture()
    await user.click(await screen.findByRole('button', { name: /^Revoke$/ }))
    const confirmation = await screen.findByRole('alertdialog')
    expect(
      within(confirmation).getByText(/Existing signed-in sessions are kept/)
    ).toBeVisible()
    await user.click(
      within(confirmation).getByRole('button', { name: 'Continue' })
    )
    await verify(user)
    await waitFor(() =>
      expect(post).toHaveBeenCalledWith(
        '/api/option/phone_login_grants',
        { phone_e164: grant.phone_e164, action: 'revoke' },
        expect.objectContaining({
          headers: { 'X-Security-Proof': 'test-step-up-proof' },
        })
      )
    )
    expect(screen.queryByText('482961')).not.toBeInTheDocument()
  })

  it('failed issuance exposes no code and does not automatically retry', async () => {
    const { user, post } = await fixture()
    post.mockImplementation(async (url) => {
      if (url === '/api/verify') {
        return {
          data: {
            success: true,
            data: {
              proof_token: 'test-step-up-proof',
              expires_at: grant.expires_at,
              scope: 'admin.user.phone_grant',
              method: 'password',
            },
          },
        }
      }
      return { data: { success: false, message: 'Please try again later.' } }
    })
    await user.click(
      await screen.findByRole('button', { name: 'New login code' })
    )
    await verify(user)
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'New login code' })
      ).toBeEnabled()
    )
    expect(
      post.mock.calls.filter(
        ([url]) => url === '/api/option/phone_login_grants'
      )
    ).toHaveLength(1)
    expect(
      screen.queryByRole('button', { name: 'Copy login code' })
    ).not.toBeInTheDocument()
  })
})
