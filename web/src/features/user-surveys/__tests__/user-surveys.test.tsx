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
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import zh from '@/i18n/locales/zh.json'
import { canAccessAdminRoute } from '@/lib/admin-permissions'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { UserSurveys } from '..'
import type { SurveyRegistrations } from '../api'

const fixture: SurveyRegistrations = {
  schema_version: 1,
  survey_id: 'product-use-v1',
  total: 1,
  options: [
    { id: 'daily_qa', label: '日常问答', label_en: 'Daily questions' },
    { id: 'coding', label: '编程开发', label_en: 'Software development' },
  ],
  items: [
    {
      user_id: 42,
      phone_e164: '+8613800000101',
      status: 'submitted',
      selected_option_ids: ['daily_qa', 'coding'],
      notes: '<script>example()</script>\n第二行备注',
      registered_at: 1793510000,
      submitted_at: 1793510060,
      deferred_at: 1793510030,
    },
  ],
}
const language = createInstance()
const clients: QueryClient[] = []
beforeEach(async () => {
  await language.init({
    lng: 'en',
    fallbackLng: 'en',
    keySeparator: false,
    nsSeparator: false,
    resources: { en, zhCN: zh },
  })
})
afterEach(() => {
  cleanup()
  clients.splice(0).forEach((client) => client.clear())
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})
function setup(
  options: {
    empty?: boolean
    fail?: boolean
    mobile?: boolean
    allowed?: boolean
    role?: number
  } = {}
) {
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'admin',
    role: options.role ?? 10,
    permissions: {
      admin_permissions: { user_surveys: { read: options.allowed ?? true } },
    },
  })
  if (options.mobile) {
    const original = window.matchMedia
    vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
      ...original(query),
      matches: query === '(max-width: 640px)',
    }))
  }
  const get = vi.spyOn(api, 'get').mockImplementation(async () => {
    if (options.fail) throw new Error('private database driver details')
    return {
      data: {
        success: true,
        data: options.empty ? { ...fixture, items: [], total: 0 } : fixture,
      },
    }
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  render(
    <I18nextProvider i18n={language}>
      <QueryClientProvider client={client}>
        <UserSurveys />
      </QueryClientProvider>
    </I18nextProvider>
  )
  return get
}
it('shows registration identity, multiple scenes and safe complete notes in shared details', async () => {
  setup()
  await screen.findByText('+8613800000101')
  expect(screen.getByText('Daily questions')).toBeVisible()
  expect(screen.getByText('Software development')).toBeVisible()
  await userEvent.click(screen.getByRole('button', { name: 'Details' }))
  const dialog = await screen.findByRole('dialog')
  expect(
    within(dialog).getByText('<script>example()</script> 第二行备注')
  ).toBeVisible()
  expect(dialog.querySelector('script')).toBeNull()
  expect(within(dialog).getByText('Deferred at')).toBeVisible()
  expect(within(dialog).getByText('Submitted at')).toBeVisible()
})
it('sends phone search only when committed and resets server pagination', async () => {
  const get = setup()
  await screen.findByText('+8613800000101')
  const input = screen.getByRole('textbox', { name: 'Phone number' })
  fireEvent.change(input, { target: { value: '00000101' } })
  expect(get).toHaveBeenCalledTimes(1)
  fireEvent.keyDown(input, { key: 'Enter' })
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith(
      '/api/user/onboarding-surveys',
      expect.objectContaining({
        params: { p: 1, page_size: 20, phone: '00000101' },
      })
    )
  )
})
it('keeps all questionnaire text and locale-sensitive formatting valid in Chinese', async () => {
  setup()
  await screen.findByText('Daily questions')
  await act(async () => language.changeLanguage('zhCN'))
  expect(await screen.findByText('日常问答')).toBeVisible()
  expect(screen.getByText('编程开发')).toBeVisible()
  expect(screen.getByText('用户问卷')).toBeVisible()
  expect(screen.queryByText('Invalid Date')).not.toBeInTheDocument()
})
it('denies the page and network request when read permission is absent or ordinary role is forged', () => {
  const get = setup({ allowed: false })
  expect(screen.getByText('Access denied')).toBeVisible()
  expect(get).not.toHaveBeenCalled()
  const ordinary = {
    id: 2,
    username: 'member',
    role: 1,
    permissions: { admin_permissions: { user_surveys: { read: true } } },
  }
  expect(canAccessAdminRoute(ordinary, '/user-surveys')).toBe(false)
  expect(canAccessAdminRoute(null, '/user-surveys')).toBe(false)
})
it('clears visible registrations when permission is revoked', async () => {
  setup()
  await screen.findByText('+8613800000101')
  act(() =>
    useAuthStore.getState().auth.setUser({
      id: 1,
      username: 'admin',
      role: 10,
      permissions: { admin_permissions: { user_surveys: { read: false } } },
    })
  )
  expect(screen.getByText('Access denied')).toBeVisible()
  expect(screen.queryByText('+8613800000101')).not.toBeInTheDocument()
})
it('shows retryable safe errors and no stale rows', async () => {
  setup({ fail: true })
  expect(
    (await screen.findAllByText('Failed to load questionnaires')).length
  ).toBeGreaterThan(0)
  expect(screen.getByRole('button', { name: 'Retry' })).toBeVisible()
  expect(
    screen.queryByText('private database driver details')
  ).not.toBeInTheDocument()
  expect(screen.queryByText('+8613800000101')).not.toBeInTheDocument()
})
it('explains why legacy accounts do not appear in an empty list', async () => {
  setup({ empty: true })
  expect(
    await screen.findByText('No questionnaire registrations')
  ).toBeVisible()
  expect(
    screen.getByText(
      'Only new phone accounts registered after this feature is enabled appear here.'
    )
  ).toBeVisible()
})
it('offers complete readable details on the mobile card view', async () => {
  setup({ mobile: true })
  await screen.findByText('+8613800000101')
  await userEvent.click(screen.getByRole('button', { name: 'Details' }))
  const dialog = await screen.findByRole('dialog')
  expect(
    within(dialog).getByText('Daily questions · Software development')
  ).toBeVisible()
  expect(within(dialog).getByText('Questionnaire status')).toBeVisible()
})

it('shows the submitted snapshot name after the current option was renamed', async () => {
  setup()
  vi.mocked(api.get).mockResolvedValue({
    data: {
      success: true,
      data: {
        ...fixture,
        options: fixture.options.map((o) => ({
          ...o,
          label_en: 'Current changed name',
        })),
        items: fixture.items.map((row) => ({
          ...row,
          selected_options: fixture.options,
        })),
      },
    },
  })
  await screen.findByText('Daily questions')
  await userEvent.click(screen.getByRole('button', { name: 'Details' }))
  const dialog = await screen.findByRole('dialog')
  expect(
    within(dialog).getByText('Daily questions · Software development')
  ).toBeVisible()
  expect(
    within(dialog).queryByText('Current changed name')
  ).not.toBeInTheDocument()
})
function editableConfig() {
  return {
    schema_version: 2,
    survey_id: 'product-use-v1',
    config_revision: 'a'.repeat(64),
    options: fixture.options.map((o) => ({ ...o, enabled: true })),
  }
}
it('saves option names, order and availability using the original configuration revision', async () => {
  setup()
  act(() =>
    useAuthStore.getState().auth.setUser({
      id: 1,
      username: 'admin',
      role: 10,
      permissions: {
        admin_permissions: { user_surveys: { read: true, write: true } },
      },
    })
  )
  const cfg = editableConfig()
  vi.mocked(api.get).mockImplementation(async (path) => ({
    data: {
      success: true,
      data: path === '/api/user/onboarding-survey-config' ? cfg : fixture,
    },
  }))
  const put = vi.spyOn(api, 'put').mockImplementation(async (_url, body) => ({
    data: {
      success: true,
      data: {
        ...cfg,
        ...(body as Record<string, unknown>),
        config_revision: 'b'.repeat(64),
      },
    },
  }))
  await userEvent.click(
    screen.getByRole('button', { name: 'Questionnaire options' })
  )
  const dialog = await screen.findByRole('dialog')
  await within(dialog).findByRole('textbox', { name: 'Chinese name 1' })
  const name = within(dialog).getByRole('textbox', { name: 'Chinese name 1' })
  await userEvent.clear(name)
  await userEvent.type(name, '新的场景名')
  await userEvent.click(
    within(dialog).getByRole('checkbox', { name: 'Enabled 1' })
  )
  await userEvent.click(
    within(dialog).getByRole('button', { name: 'Move down 1' })
  )
  await userEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith(
      '/api/user/onboarding-survey-config',
      expect.objectContaining({
        schema_version: 2,
        expected_config_revision: 'a'.repeat(64),
        options: [
          cfg.options[1],
          { ...cfg.options[0], label: '新的场景名', enabled: false },
        ],
      })
    )
  )
})
it('preserves an administrator draft when the server reports a revision conflict', async () => {
  setup()
  act(() =>
    useAuthStore.getState().auth.setUser({
      id: 1,
      username: 'admin',
      role: 10,
      permissions: {
        admin_permissions: { user_surveys: { read: true, write: true } },
      },
    })
  )
  const cfg = editableConfig()
  vi.mocked(api.get).mockImplementation(async (path) => ({
    data: {
      success: true,
      data: path === '/api/user/onboarding-survey-config' ? cfg : fixture,
    },
  }))
  vi.spyOn(api, 'put').mockResolvedValue({
    data: { success: false, code: 'SURVEY_CONFIG_CHANGED' },
  })
  await userEvent.click(
    screen.getByRole('button', { name: 'Questionnaire options' })
  )
  const dialog = await screen.findByRole('dialog')
  const name = await within(dialog).findByRole('textbox', {
    name: 'Chinese name 1',
  })
  await userEvent.clear(name)
  await userEvent.type(name, '保留的草稿')
  await userEvent.click(within(dialog).getByRole('button', { name: 'Save' }))
  await within(dialog).findByText(/Options were not saved/)
  expect(name).toHaveValue('保留的草稿')
})
it('lets a read-only operator view options while preventing editing', async () => {
  setup()
  const cfg = editableConfig()
  vi.mocked(api.get).mockImplementation(async (path) => ({
    data: {
      success: true,
      data: path === '/api/user/onboarding-survey-config' ? cfg : fixture,
    },
  }))
  const put = vi.spyOn(api, 'put')
  await userEvent.click(
    screen.getByRole('button', { name: 'Questionnaire options' })
  )
  const dialog = await screen.findByRole('dialog')
  const input = await within(dialog).findByRole('textbox', {
    name: 'Chinese name 1',
  })
  expect(input).toBeDisabled()
  expect(within(dialog).getByRole('button', { name: 'Save' })).toBeDisabled()
  expect(put).not.toHaveBeenCalled()
})
