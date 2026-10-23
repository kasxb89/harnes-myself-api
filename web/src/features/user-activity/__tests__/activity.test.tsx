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
  act,
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
import type {
  ClientActivityDetail,
  ClientActivitySession,
  ClientActivitySummary,
} from '../types'

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
    crossMidnight?: boolean
    summaryData?: ClientActivitySummary
    detailData?: ClientActivityDetail
  } = {}
) {
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'admin',
    role: options.role ?? ROLE.ADMIN,
    permissions: { admin_permissions: { user_activity: { read: true } } },
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
    let data: unknown = options.summaryData ?? summary
    if (url === '/api/client-activity/sessions') {
      const params = config?.params as {
        username?: string
        scope?: string
        start_timestamp?: number
        end_timestamp?: number
      }
      const items = options.empty
        ? []
        : sessions
            .map((row) =>
              options.crossMidnight && row.id === 1
                ? {
                    ...row,
                    started_at: Math.floor(Date.now() / 1000) - 86400,
                    last_seen_at: Math.floor(Date.now() / 1000),
                  }
                : row
            )
            .filter((row) => {
              if (params.username && row.username !== params.username) {
                return false
              }
              if (params.scope === 'live' && !row.online) return false
              if (
                options.crossMidnight &&
                params.start_timestamp &&
                row.started_at < params.start_timestamp
              ) {
                return false
              }
              return true
            })
      data = { items, total: items.length }
    } else if (url === '/api/client-activity/sessions/1') {
      data = options.detailData ?? {
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
  expect(screen.queryByText('carol')).not.toBeInTheDocument()
  await userEvent.click(
    screen.getByRole('tab', { name: 'Historical sessions' })
  )
  expect(await screen.findByText('carol')).toBeVisible()
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
    screen.getByRole('tab', { name: 'Historical sessions' })
  )
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

function workloadSummary(
  coverage: 'complete' | 'partial' | 'not_collected' = 'complete'
): ClientActivitySummary {
  const item = {
    collected_runtime_sessions:
      coverage === 'complete' ? 3 : Number(coverage === 'partial'),
    coverage,
  }
  return {
    ...summary,
    runtime_sessions: coverage === 'partial' ? 4 : 3,
    workload: {
      schema_version: 1,
      server_time: summary.server_time,
      scope: 'global_live',
      known: {
        generating_chat_sessions: 1,
        projects: 1,
        planned_shots: 8,
        task_attempts: 8,
        accepted_tasks: 2,
        queued_tasks: 3,
        generating_tasks: 2,
        saving_tasks: 1,
        editing_projects: 0,
        local_operations: 0,
      },
      uncertainty: {
        unknown_task_attempts: 0,
        conflicting_task_attempts: 0,
        unknown_chat_sessions: 0,
        unknown_projects: 0,
        expired_runtime_sessions: 0,
        unreported_runtime_sessions: 0,
      },
      coverage: {
        online_runtime_sessions: coverage === 'partial' ? 4 : 3,
        reporting_runtime_sessions:
          coverage === 'complete' ? 3 : Number(coverage === 'partial'),
        collections: {
          chat_activity: { ...item },
          project_run: { ...item },
          storyboard_task: { ...item },
          local_operation: { ...item },
          shots: { ...item },
        },
      },
    },
  }
}

it('defaults to current online scope across midnight without sending a started-today range', async () => {
  const get = setup({ crossMidnight: true })
  expect(await screen.findByText('alice')).toBeVisible()
  expect(screen.getByRole('tab', { name: 'Currently online' })).toHaveAttribute(
    'aria-selected',
    'true'
  )
  const params = get.mock.calls.find(
    ([path]) => path === '/api/client-activity/sessions'
  )?.[1]?.params
  expect(params).toMatchObject({ scope: 'live' })
  expect(params).not.toHaveProperty('start_timestamp')
  expect(params).not.toHaveProperty('end_timestamp')
  expect(
    screen.queryByRole('button', { name: /^\d{4}-\d{2}-\d{2}/ })
  ).not.toBeInTheDocument()
})

it('history exposes the selected period and explicitly switches last-seen versus started query basis', async () => {
  const get = setup()
  const user = userEvent.setup()
  await user.click(
    await screen.findByRole('tab', { name: 'Historical sessions' })
  )
  await waitFor(() =>
    expect(
      [...get.mock.calls]
        .reverse()
        .find(([path]) => path === '/api/client-activity/sessions')?.[1]?.params
    ).toMatchObject({
      scope: 'history',
      time_basis: 'last_seen',
      start_timestamp: expect.any(Number),
      end_timestamp: expect.any(Number),
    })
  )
  await user.click(screen.getByRole('combobox', { name: 'History time basis' }))
  await user.click(await screen.findByRole('option', { name: 'Session start' }))
  await waitFor(() =>
    expect(
      [...get.mock.calls]
        .reverse()
        .find(([path]) => path === '/api/client-activity/sessions')?.[1]?.params
    ).toMatchObject({ scope: 'history', time_basis: 'started' })
  )
})

it('an older API without workload shows not collected instead of invented generating zeros', async () => {
  setup()
  await screen.findByText('alice')
  const workload = await screen.findByRole('region', {
    name: 'Observed workload',
  })
  expect(within(workload).getAllByText('Not collected').length).toBeGreaterThan(
    0
  )
  expect(within(workload).queryByText('0')).not.toBeInTheDocument()
})

it('one known generating chat with eight media attempts keeps exact task phases separate', async () => {
  setup({ summaryData: workloadSummary() })
  await screen.findByText('alice')
  const workload = await screen.findByRole('region', {
    name: 'Observed workload',
  })
  expect(
    within(
      within(workload).getByRole('group', {
        name: testI18n.t('Generating chat sessions'),
      })
    ).getByText('1')
  ).toBeVisible()
  expect(
    within(
      within(workload).getByRole('group', { name: 'Media task attempts' })
    ).getByText('8')
  ).toBeVisible()
  expect(
    within(workload).getByRole('row', { name: /Queued tasks 3/ })
  ).toBeVisible()
  expect(
    within(workload).getByRole('row', { name: /Provider-generating tasks 2/ })
  ).toBeVisible()
})

it('measured zero, uncollected work and uncertain partial observations remain distinct', async () => {
  const data = workloadSummary('partial')
  if (!data.workload) throw new Error('fixture workload missing')
  data.workload.known.generating_chat_sessions = 0
  data.workload.known.local_operations = 0
  data.workload.coverage.collections.local_operation = {
    coverage: 'not_collected',
    collected_runtime_sessions: 0,
  }
  data.workload.uncertainty.unknown_task_attempts = 2
  data.workload.uncertainty.conflicting_task_attempts = 1
  data.workload.uncertainty.expired_runtime_sessions = 1
  data.workload.uncertainty.unreported_runtime_sessions = 2
  setup({ summaryData: data })
  await screen.findByText('alice')
  const workload = await screen.findByRole('region', {
    name: 'Observed workload',
  })
  expect(
    within(
      within(workload).getByRole('group', {
        name: testI18n.t('Generating chat sessions'),
      })
    ).getByText('Known count: 0')
  ).toBeVisible()
  expect(
    within(
      within(workload).getByRole('group', { name: 'Local operations' })
    ).getByText('Not collected')
  ).toBeVisible()
  expect(
    within(workload).getByRole('row', { name: /Unknown task attempts 2/ })
  ).toBeVisible()
  expect(
    within(workload).getByRole('row', { name: /Conflicting task attempts 1/ })
  ).toBeVisible()
  expect(
    within(workload).getByRole('row', {
      name: /Expired runtime observations 1/,
    })
  ).toBeVisible()
  expect(
    within(workload).getByRole('row', { name: /Unreported client processes 2/ })
  ).toBeVisible()
})

it('revoking activity permission removes cached sessions and workload immediately', async () => {
  setup({ summaryData: workloadSummary() })
  await screen.findByText('alice')
  act(() =>
    useAuthStore.getState().auth.setUser({
      id: 1,
      username: 'admin',
      role: ROLE.ADMIN,
      permissions: { admin_permissions: { user_activity: { read: false } } },
    })
  )
  expect(screen.getByText('Access denied')).toBeVisible()
  expect(screen.queryByText('alice')).not.toBeInTheDocument()
  expect(
    screen.queryByRole('region', { name: 'Observed workload' })
  ).not.toBeInTheDocument()
})

it('shows a complete measured zero and retains known positive chats from associated creative work', async () => {
  const data = workloadSummary()
  if (!data.workload) throw new Error('fixture workload missing')
  data.workload.coverage.collections.chat_activity = {
    coverage: 'not_collected',
    collected_runtime_sessions: 0,
  }
  setup({ summaryData: data })
  await screen.findByText('alice')
  const workload = await screen.findByRole('region', {
    name: 'Observed workload',
  })
  await within(workload).findByText('Known count: 1')
  expect(
    within(
      within(workload).getByRole('group', {
        name: testI18n.t('Generating chat sessions'),
      })
    ).getByText('Known count: 1')
  ).toBeVisible()
  expect(
    within(
      within(workload).getByRole('group', { name: 'Local operations' })
    ).getByText('0')
  ).toBeVisible()
})

it('renders only safe workload detail fields and warns that expired or truncated observations are not upstream outcomes', async () => {
  const privateFields = {
    login_session_id: 'private-login',
    digest: 'private-digest',
    prompt: 'private-conversation',
    phase_description: 'private-description',
  }
  const detail: ClientActivityDetail = {
    session: { ...sessions[0], observation_detail: 'expired' },
    usage: { calls: 0, errors: 0, models: [] },
    workload: {
      schema_version: 1,
      observation_status: 'expired',
      accepted_at: summary.server_time,
      accepted_sequence: 42,
      collections: {
        chat_activity: 'not_collected',
        project_run: 'collected',
        storyboard_task: 'collected',
        local_operation: 'not_collected',
        shots: 'collected',
      },
      activities: [
        {
          activity_id: 'attempt-safe',
          kind: 'storyboard_task' as const,
          work_state: 'unknown' as const,
          phase: 'unknown',
          observation_status: 'unknown' as const,
          chat_session_id: 'root-safe',
          project_id: 'project-safe',
          attempt_id: 'durable-safe',
          api_task_id: 'provider-safe',
          ...privateFields,
        },
      ],
      shots: [
        {
          project_id: 'project-safe',
          shot_id: 'shot-1',
          observation_status: 'expired',
        },
      ],
      truncated: true,
    },
  }
  setup({ detailData: detail })
  await userEvent.click(
    await screen.findByRole('button', { name: 'View activity for alice' })
  )
  const dialog = await screen.findByRole('dialog')
  expect(await within(dialog).findByText('root-safe')).toBeVisible()
  expect(within(dialog).getByText('durable-safe')).toBeVisible()
  expect(within(dialog).getByText('provider-safe')).toBeVisible()
  expect(within(dialog).getByText('shot-1')).toBeVisible()
  expect(within(dialog).getByText(/most recent 256/)).toBeVisible()
  expect(within(dialog).getByText(/not provider settlement/)).toBeVisible()
  for (const secret of [
    'private-login',
    'private-digest',
    'private-conversation',
    'private-description',
  ]) {
    expect(within(dialog).queryByText(secret)).not.toBeInTheDocument()
  }
  expect(
    within(dialog).queryByText('Reported cancellation')
  ).not.toBeInTheDocument()
})

it('isolates cached rows and global workload when the signed-in administrator changes', async () => {
  const get = setup({ summaryData: workloadSummary() })
  await screen.findByText('alice')
  const replacement = workloadSummary()
  if (!replacement.workload) throw new Error('fixture workload missing')
  replacement.workload.known.generating_chat_sessions = 0
  get.mockImplementation(async (url) => ({
    data: {
      success: true,
      data:
        url === '/api/client-activity/sessions'
          ? { items: [sessions[1]], total: 1 }
          : replacement,
    },
  }))
  act(() =>
    useAuthStore.getState().auth.setUser({
      id: 2,
      username: 'second-admin',
      role: ROLE.ADMIN,
      permissions: { admin_permissions: { user_activity: { read: true } } },
    })
  )
  expect(screen.queryByText('alice')).not.toBeInTheDocument()
  await screen.findByText('bob')
  const workload = screen.getByRole('region', { name: 'Observed workload' })
  expect(
    await within(
      within(workload).getByRole('group', {
        name: testI18n.t('Generating chat sessions'),
      })
    ).findByText('0')
  ).toBeVisible()
})
