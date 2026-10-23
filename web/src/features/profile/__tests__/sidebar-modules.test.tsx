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
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AxiosError, AxiosHeaders, type AxiosAdapter } from 'axios'
import { afterEach, expect, it } from 'vitest'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { SidebarModulesCard } from '../components/sidebar-modules-card'

const originalAdapter = api.defaults.adapter
afterEach(() => {
  cleanup()
  api.defaults.adapter = originalAdapter
  useAuthStore.getState().auth.reset()
})
function account() {
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'admin',
    role: ROLE.ADMIN,
    permissions: { admin_permissions: { model_profits: { read: true } } },
  })
}
it('loads legacy preferences, exposes business modules and saves independent toggles without dropping old keys', async () => {
  account()
  const user = userEvent.setup()
  let saved: Record<string, Record<string, boolean>> | undefined
  api.defaults.adapter = (async (config) => {
    if (config.method === 'get') {
      return {
        data: {
          success: true,
          data: {
            sidebar_modules:
              '{"admin":{"enabled":true,"setting":false,"future":false}}',
          },
        },
        status: 200,
        statusText: 'OK',
        headers: new AxiosHeaders(),
        config,
      }
    }
    const body = JSON.parse(config.data)
    saved = JSON.parse(body.sidebar_modules)
    return {
      data: { success: true },
      status: 200,
      statusText: 'OK',
      headers: new AxiosHeaders(),
      config,
    }
  }) satisfies AxiosAdapter
  render(<SidebarModulesCard />)
  const profits = await screen.findByRole('switch', { name: 'Model profits' })
  expect(profits).toHaveAttribute('aria-checked', 'true')
  expect(
    screen.queryByRole('switch', { name: 'System Settings' })
  ).not.toBeInTheDocument()
  await user.click(profits)
  await user.click(screen.getByRole('button', { name: 'Save Changes' }))
  await waitFor(() => expect(saved?.finance.model_profits).toBe(false))
  expect(saved?.admin.future).toBe(false)
  expect(saved?.system.setting).toBe(false)
  expect(
    JSON.parse(useAuthStore.getState().auth.user?.sidebar_modules ?? '{}')
      .finance.model_profits
  ).toBe(false)
})
it('blocks saving while loading or after a failed load and allows a deliberate retry', async () => {
  account()
  const user = userEvent.setup()
  let fail = true
  let puts = 0
  let finish: (() => void) | undefined
  api.defaults.adapter = (async (config) => {
    if (config.method === 'put') {
      puts++
      throw new Error('Unexpected save')
    }
    await new Promise<void>((resolve) => {
      finish = resolve
    })
    if (fail) {
      throw new AxiosError('fixture network failure', 'ERR_NETWORK', config)
    }
    return {
      data: { success: true, data: { sidebar_modules: '' } },
      status: 200,
      statusText: 'OK',
      headers: new AxiosHeaders(),
      config,
    }
  }) satisfies AxiosAdapter
  render(<SidebarModulesCard />)
  expect(screen.getByRole('button', { name: 'Save Changes' })).toBeDisabled()
  await waitFor(() => expect(finish).toBeTypeOf('function'))
  await act(async () => {
    finish?.()
    finish = undefined
  })
  expect(
    await screen.findByText('Failed to load sidebar settings')
  ).toBeVisible()
  expect(screen.getByRole('button', { name: 'Save Changes' })).toBeDisabled()
  expect(puts).toBe(0)
  fail = false
  await user.click(screen.getByRole('button', { name: 'Retry' }))
  await waitFor(() => expect(finish).toBeTypeOf('function'))
  await act(async () => {
    finish?.()
  })
  expect(
    await screen.findByRole('switch', { name: 'Model profits' })
  ).toBeVisible()
  expect(screen.getByRole('button', { name: 'Save Changes' })).toBeEnabled()
})
