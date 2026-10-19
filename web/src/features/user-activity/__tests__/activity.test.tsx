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
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { Route } from '@/routes/_authenticated/user-activity'
import { useAuthStore } from '@/stores/auth-store'

import { UserActivity } from '..'
import type { ClientActivitySession, ClientActivitySummary } from '../types'

const summary: ClientActivitySummary = {
  server_time: 1792398125,
  heartbeat_interval_seconds: 30,
  online_timeout_seconds: 90,
  online_users: 3,
  foreground_users: 1,
  started_sessions: 4,
  active_users: 3,
  online_seconds: 1800,
  foreground_seconds: 300,
  model_calls: 7,
  model_errors: 2,
}
const sessions: ClientActivitySession[] = [
  {
    id: 1,
    user_id: 42,
    username: 'alice',
    session_id: 'launch-a',
    platform: 'win32',
    app_version: '0.2.1-alpha.20261019',
    started_at: 1792398000,
    last_seen_at: 1792398125,
    ended_at: 0,
    online_seconds: 125,
    foreground_seconds: 25,
    foreground: true,
    online: true,
    status: 'foreground',
    model_calls: 2,
    model_errors: 1,
    last_model_at: 1792398110,
  },
  {
    id: 2,
    user_id: 43,
    username: 'bob',
    session_id: 'launch-b',
    platform: 'darwin',
    app_version: '0.2.1',
    started_at: 1792398000,
    last_seen_at: 1792398120,
    ended_at: 0,
    online_seconds: 120,
    foreground_seconds: 0,
    foreground: false,
    online: true,
    status: 'background',
    model_calls: 0,
    model_errors: 0,
    last_model_at: 0,
  },
  {
    id: 3,
    user_id: 44,
    username: 'carol',
    session_id: 'launch-c',
    platform: 'linux',
    app_version: '0.2.1',
    started_at: 1792397000,
    last_seen_at: 1792397100,
    ended_at: 1792397110,
    online_seconds: 100,
    foreground_seconds: 20,
    foreground: false,
    online: false,
    status: 'offline',
    model_calls: 0,
    model_errors: 0,
    last_model_at: 0,
  },
]
const testI18n = createInstance()
beforeEach(async () => {
  await testI18n.init({
    lng: 'en',
    fallbackLng: 'en',
    keySeparator: false,
    nsSeparator: false,
    resources: { en },
  })
})
const clients: QueryClient[] = []
function setup(
  options: {
    empty?: boolean
    fail?: boolean
    role?: number
    mobile?: boolean
  } = {}
) {
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'admin',
    role: options.role ?? ROLE.ADMIN,
  })
  if (options.mobile) {
    const original = window.matchMedia
    vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
      ...original(query),
      matches: query === '(max-width: 640px)',
    }))
  }
  const get = vi.spyOn(api, 'get').mockImplementation(async (url, config) => {
    if (options.fail) throw new Error('Network unreachable')
    let data: unknown = summary
    if (url === '/api/client-activity/sessions') {
      const params = config?.params as { username?: string }
      const items = options.empty
        ? []
        : sessions.filter(
            (row) => !params.username || row.username === params.username
          )
      data = { items, total: items.length }
    } else if (url === '/api/client-activity/sessions/1') {
      data = {
        session: sessions[0],
        usage: {
          calls: 2,
          errors: 1,
          models: [
            {
              model_name: 'deepseek-flash',
              calls: 2,
              errors: 1,
              last_called_at: 1792398110,
            },
          ],
        },
      }
    }
    return { data: { success: true, data } }
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  render(
    <I18nextProvider i18n={testI18n}>
      <QueryClientProvider client={client}>
        <UserActivity />
      </QueryClientProvider>
    </I18nextProvider>
  )
  return get
}
afterEach(() => {
  cleanup()
  clients.splice(0).forEach((client) => client.clear())
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})

it('separates foreground, background presence, offline and real model usage', async () => {
  setup()
  await screen.findByText('alice')
  expect(screen.getByText('bob')).toBeVisible()
  expect(screen.getByText('carol')).toBeVisible()
  expect(screen.getByText('Active foreground')).toBeVisible()
  expect(screen.getByText('Background or idle')).toBeVisible()
  expect(screen.getByText('Offline')).toBeVisible()
  expect(screen.getByText('Actual model calls in range')).toBeVisible()
  expect(screen.getByText(/Clients without reporting/)).toBeVisible()
  expect(
    screen.getAllByText(/2026-10-19 \d{2}:\d{2}:\d{2} [+-]\d{2}:\d{2}/).length
  ).toBeGreaterThan(0)
})
it('opens real observed timestamps and model totals without conversation contents', async () => {
  setup()
  await userEvent.click(
    await screen.findByRole('button', { name: 'View activity for alice' })
  )
  const dialog = await screen.findByRole('dialog')
  await within(dialog).findByText('deepseek-flash')
  expect(
    within(dialog).getByText('Client session reporting started')
  ).toBeVisible()
  expect(within(dialog).getByText('Last heartbeat observed')).toBeVisible()
  expect(
    within(dialog).queryByText('Client session ended')
  ).not.toBeInTheDocument()
  expect(within(dialog).getByText(/may include other devices/)).toBeVisible()
})
it('commits username on search while keeping global summary separate', async () => {
  const get = setup()
  await screen.findByText('alice')
  await userEvent.type(screen.getByRole('textbox', { name: 'Username' }), 'bob')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(screen.queryByText('alice')).not.toBeInTheDocument()
  )
  expect(screen.getByText('bob')).toBeVisible()
  const sessionCalls = get.mock.calls.filter(
    ([path]) => path === '/api/client-activity/sessions'
  )
  expect(sessionCalls.at(-1)?.[1]?.params).toMatchObject({
    username: 'bob',
    p: 1,
  })
  const summaryCall = get.mock.calls.find(
    ([path]) => path === '/api/client-activity/summary'
  )
  expect(summaryCall?.[1]?.params).not.toHaveProperty('username')
})
it('keeps the empty state explicit about clients that do not report', async () => {
  setup({ empty: true })
  expect(await screen.findByText('No client sessions')).toBeVisible()
  expect(
    screen.getByText(
      'Only reporting clients appear here. Try another user or period.'
    )
  ).toBeVisible()
})
it('shows a retry control after activity data fails instead of showing cached rows', async () => {
  setup({ fail: true })
  expect(await screen.findByRole('button', { name: 'Retry' })).toBeVisible()
  expect(screen.queryByText('alice')).not.toBeInTheDocument()
})
it('does not request client or model data when an ordinary user opens the component', async () => {
  const get = setup({ role: ROLE.USER })
  expect(await screen.findByText('Access denied')).toBeVisible()
  expect(get).not.toHaveBeenCalled()
})
it('redirects ordinary users from the route to the forbidden page', async () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 42, username: 'alice', role: ROLE.USER })
  await expect(async () =>
    Route.options.beforeLoad?.({} as never)
  ).rejects.toMatchObject({ options: { to: '/403' } })
})
it('shows readable client states and accessible details on mobile cards', async () => {
  setup({ mobile: true })
  await screen.findByText('alice')
  expect(
    screen.getByRole('button', { name: 'View activity for alice' })
  ).toBeVisible()
  expect(screen.getByText('Background or idle')).toBeVisible()
  expect(screen.queryByRole('table')).not.toBeInTheDocument()
})

it('does not send an invalid date range and hides the previous sessions', async () => {
  const get = setup()
  await screen.findByText('alice')
  await userEvent.click(
    screen.getByRole('button', { name: /^\d{4}-\d{2}-\d{2}/ })
  )
  fireEvent.change(screen.getByLabelText('Start Time'), {
    target: { value: '2026-10-20T12:00:00' },
  })
  fireEvent.change(screen.getByLabelText('End Time'), {
    target: { value: '2026-10-19T12:00:00' },
  })
  const requests = get.mock.calls.length
  await userEvent.click(screen.getByRole('button', { name: 'Confirm' }))
  expect(
    await screen.findByText('End time must be after start time')
  ).toBeVisible()
  expect(screen.queryByText('alice')).not.toBeInTheDocument()
  expect(get).toHaveBeenCalledTimes(requests)
  expect(screen.getByRole('button', { name: 'Refresh' })).toBeDisabled()
})
it('requests only background sessions when the resident status is chosen', async () => {
  const get = setup()
  await screen.findByText('alice')
  await userEvent.click(screen.getByRole('combobox', { name: 'Client status' }))
  await userEvent.click(
    await screen.findByRole('option', { name: 'Background or idle' })
  )
  await waitFor(() => {
    const last = [...get.mock.calls]
      .reverse()
      .find(([path]) => path === '/api/client-activity/sessions')
    expect(last?.[1]?.params).toMatchObject({ status: 'background', p: 1 })
  })
})
