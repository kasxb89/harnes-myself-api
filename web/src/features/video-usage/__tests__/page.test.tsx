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
import { AxiosError, AxiosHeaders, type AxiosAdapter } from 'axios'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { Route } from '@/routes/_authenticated/video-usage'
import { useAuthStore } from '@/stores/auth-store'

import { VideoUsagePage } from '..'
import { emptyVideoPayload, videoPayload } from './fixtures'

const clients: QueryClient[] = []
const originalAdapter = api.defaults.adapter
const i18n = createInstance()
beforeEach(async () => {
  await i18n.init({
    lng: 'en',
    fallbackLng: 'en',
    keySeparator: false,
    nsSeparator: false,
    resources: { en },
  })
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'admin',
    role: ROLE.ADMIN,
    permissions: { admin_permissions: { video_usage: { read: true } } },
  })
})
afterEach(() => {
  cleanup()
  clients.splice(0).forEach((client) => client.clear())
  useAuthStore.getState().auth.reset()
  api.defaults.adapter = originalAdapter
  vi.useRealTimers()
  vi.restoreAllMocks()
})
function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  return render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={client}>
        <VideoUsagePage />
      </QueryClientProvider>
    </I18nextProvider>
  )
}
function mockResult(data = videoPayload) {
  return vi.spyOn(api, 'get').mockImplementation(async (_url, options) => ({
    data: {
      success: true,
      data: {
        ...data,
        tasks: {
          ...data.tasks,
          page: options?.params.p ?? 1,
          page_size: options?.params.page_size ?? 20,
          scope: options?.params.task_scope ?? 'history',
        },
      },
    },
  }))
}

it('restoring read permission starts a fresh request while a cancelled identical request is still settling', async () => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(new Date('2026-10-24T02:00:00Z'))
  const aborted = vi.fn()
  const requests: {
    signal: { aborted?: boolean } | undefined
    complete: (data: unknown) => void
  }[] = []
  const adapter: AxiosAdapter = (config) =>
    new Promise((resolve) => {
      config.signal?.addEventListener?.('abort', aborted, { once: true })
      requests.push({
        signal: config.signal,
        complete: (data) =>
          resolve({
            config,
            data: { success: true, data },
            status: 200,
            statusText: 'OK',
            headers: {},
          }),
      })
    })
  api.defaults.adapter = adapter
  try {
    renderPage()
    await waitFor(() => expect(requests).toHaveLength(1))
    await act(async () => {
      useAuthStore.getState().auth.setUser({
        id: 1,
        username: 'admin',
        role: ROLE.ADMIN,
        permissions: {
          admin_permissions: { video_usage: { read: false } },
        },
      })
    })
    expect(screen.getByText('Access denied')).toBeVisible()
    clients[0].removeQueries({ queryKey: ['video-usage'] })
    expect(clients[0].getQueryCache().getAll()).toHaveLength(0)
    await act(async () => {
      useAuthStore.getState().auth.setUser({
        id: 1,
        username: 'admin',
        role: ROLE.ADMIN,
        permissions: {
          admin_permissions: { video_usage: { read: true } },
        },
      })
    })
    await waitFor(() => expect(requests).toHaveLength(2))
    expect(requests[0].signal?.aborted).toBe(true)
    expect(aborted).toHaveBeenCalledOnce()
    await act(async () => requests[0].complete(videoPayload))
    expect(screen.queryByText('MiniMax-H3')).not.toBeInTheDocument()
    expect(
      screen.getByRole('status', { name: 'Loading video usage' })
    ).toBeVisible()
    await act(async () => requests[1].complete(emptyVideoPayload))
    expect(await screen.findByText('No recorded video tasks')).toBeVisible()
  } finally {
    await act(async () =>
      requests.forEach((request) => request.complete(emptyVideoPayload))
    )
  }
})

it('ordinary users cannot enter the route or request video usage data', () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 2, username: 'user', role: ROLE.USER })
  const get = mockResult()
  expect(() => (Route.options.beforeLoad as () => void)()).toThrow()
  renderPage()
  expect(screen.getByText('Access denied')).toBeVisible()
  expect(get).not.toHaveBeenCalled()
})

it('current records distinguish known in-flight states, unknown states and excluded peak samples', async () => {
  mockResult()
  renderPage()
  const table = await screen.findByRole('table', {
    name: 'Current video task records',
  })
  expect(
    within(table).getByRole('row', { name: /MiniMax-H3/ })
  ).toHaveTextContent('4')
  const unknown = within(table).getByRole('row', { name: /bt-seedance-2.0/ })
  expect(
    within(unknown)
      .getAllByRole('cell')
      .map((cell) => cell.textContent)
  ).toEqual(['bt-seedance-2.0', '0', '0', '0', '1'])
  expect(
    screen.getByText('Peak intervals: 5 valid / 3 excluded.')
  ).toBeVisible()
  expect(
    screen.getByText('Turnaround samples: 3 valid / 2 missing / 1 invalid.')
  ).toBeVisible()
  expect(
    screen.getByText(
      'Upstream capacity is unknown. Client online time is not attributed to video models.'
    )
  ).toBeVisible()
  expect(screen.queryByText('0%')).not.toBeInTheDocument()
})

it('the period summary shows durations and sample quality without turning missing values into zero', async () => {
  mockResult()
  renderPage()
  await screen.findByRole('table', { name: 'Current video task records' })
  await userEvent.click(screen.getByRole('tab', { name: 'Period summary' }))
  const table = screen.getByRole('table', { name: 'Video usage by model' })
  const known = within(table).getByRole('row', { name: /MiniMax-H3/ })
  expect(known).toHaveTextContent('80 s')
  expect(known).toHaveTextContent('70 s')
  expect(known).toHaveTextContent('100 s')
  expect(known).toHaveTextContent('3 valid (0 zero) / 2 missing / 1 invalid')
  const missing = within(table).getByRole('row', { name: /bt-seedance-2.0/ })
  expect(missing).toHaveTextContent('Unknown')
  expect(missing).not.toHaveTextContent('0 s')
})

it('task details preserve a missing start and running duration while marking source declarations', async () => {
  mockResult()
  renderPage()
  await screen.findByRole('table', { name: 'Current video task records' })
  await userEvent.click(screen.getByRole('tab', { name: 'Task details' }))
  const missing = screen.getByRole('row', { name: /task-102/ })
  expect(missing).toHaveTextContent('Timing missing')
  expect(missing).toHaveTextContent('Observed start: Unknown')
  expect(missing).toHaveTextContent('#8')
  expect(missing).not.toHaveTextContent('0 s')
  const declared = screen.getByRole('row', { name: /task-101/ })
  expect(declared).toHaveTextContent('Desktop client declaration')
  expect(declared).toHaveTextContent('Upstream H3 usage report')
  expect(declared).toHaveTextContent('10 s')
  expect(declared).toHaveTextContent('H3 Metaso (#12)')
  expect(declared).toHaveTextContent('Metaso')
  const invalid = screen.getByRole('row', { name: /task-103/ })
  expect(invalid).toHaveTextContent('Timing inconsistent')
  expect(invalid).not.toHaveTextContent('0 s')
})

it('server pagination, task scope and four explicit filters keep their exact query contract', async () => {
  const get = mockResult()
  renderPage()
  await screen.findByRole('table', { name: 'Current video task records' })
  const user = userEvent.setup()
  await user.click(screen.getByRole('tab', { name: 'Task details' }))
  await user.click(screen.getByRole('button', { name: 'Go to next page' }))
  await waitFor(() => expect(get.mock.lastCall?.[1]?.params.p).toBe(2))
  expect(screen.getByRole('tab', { name: 'Task details' })).toHaveAttribute(
    'aria-selected',
    'true'
  )
  await user.click(screen.getByRole('combobox', { name: 'Task list scope' }))
  await user.click(
    await screen.findByRole('option', { name: 'Current nonterminal records' })
  )
  await waitFor(() =>
    expect(get.mock.lastCall?.[1]?.params).toEqual(
      expect.objectContaining({ p: 1, task_scope: 'current' })
    )
  )
  await user.click(screen.getByRole('combobox', { name: 'User' }))
  await user.click(await screen.findByRole('option', { name: 'Alice (#7)' }))
  await user.click(screen.getByRole('combobox', { name: 'Model' }))
  await user.click(await screen.findByRole('option', { name: 'MiniMax-H3' }))
  await user.click(screen.getByRole('combobox', { name: 'Client source' }))
  await user.click(
    await screen.findByRole('option', { name: 'Desktop client declaration' })
  )
  expect(get).toHaveBeenCalledTimes(3)
  await user.click(screen.getByRole('button', { name: 'Apply filters' }))
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith(
      '/api/video-usage',
      expect.objectContaining({
        params: expect.objectContaining({
          user_id: 7,
          model: 'MiniMax-H3',
          client_source: 'desktop',
          task_scope: 'current',
          p: 1,
          page_size: 20,
        }),
      })
    )
  )
  expect(
    screen.getByText(
      'Source coverage before source filtering: 2 desktop declarations / 5 unknown / 7 records.'
    )
  ).toBeVisible()
})

it('narrow responses preserve previously loaded directory choices without an extra directory request', async () => {
  const get = mockResult()
  renderPage()
  await screen.findByRole('table', { name: 'Current video task records' })
  const user = userEvent.setup()
  await user.click(screen.getByRole('combobox', { name: 'Model' }))
  await user.click(await screen.findByRole('option', { name: 'MiniMax-H3' }))
  get.mockResolvedValueOnce({
    data: {
      success: true,
      data: {
        ...videoPayload,
        filter_options: {
          ...videoPayload.filter_options,
          models: ['MiniMax-H3'],
          users: [videoPayload.filter_options.users[0]],
        },
      },
    },
  })
  await user.click(screen.getByRole('button', { name: 'Apply filters' }))
  await waitFor(() => expect(get).toHaveBeenCalledTimes(2))
  await user.click(screen.getByRole('combobox', { name: 'Model' }))
  expect(
    await screen.findByRole('option', { name: 'bt-seedance-2.0' })
  ).toBeVisible()
  expect(get).toHaveBeenCalledTimes(2)
})

it('pending and failed requests have safe distinct states and retry can recover an empty report', async () => {
  const get = vi
    .spyOn(api, 'get')
    .mockImplementationOnce(() => new Promise(() => {}))
  const first = renderPage()
  expect(
    screen.getByRole('status', { name: 'Loading video usage' })
  ).toBeVisible()
  expect(screen.queryByText('No recorded video tasks')).not.toBeInTheDocument()
  first.unmount()
  clients.splice(0).forEach((client) => client.clear())
  get.mockRejectedValueOnce(new Error('private raw database failure'))
  renderPage()
  expect(await screen.findByText('Failed to load video usage')).toBeVisible()
  expect(
    screen.queryByText('private raw database failure')
  ).not.toBeInTheDocument()
  get.mockResolvedValueOnce({
    data: { success: true, data: emptyVideoPayload },
  })
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(await screen.findByText('No recorded video tasks')).toBeVisible()
})

it.each([
  { status: 422, code: 'VIDEO_USAGE_QUERY_TOO_BROAD', special: true },
  { status: 500, code: 'VIDEO_USAGE_QUERY_TOO_BROAD', special: false },
  { status: 422, code: 'VIDEO_USAGE_QUERY_FAILED', special: false },
])(
  'status $status and code $code use only the approved safe error',
  async ({ status, code, special }) => {
    const config = { headers: new AxiosHeaders() }
    vi.spyOn(api, 'get').mockRejectedValue(
      new AxiosError(
        'private transport detail',
        'ERR_BAD_REQUEST',
        config,
        undefined,
        {
          status,
          data: { success: false, code, message: 'private response text' },
          statusText: 'Error',
          headers: {},
          config,
        }
      )
    )
    renderPage()
    await screen.findByText('Failed to load video usage')
    expect(
      screen.queryByText('Narrow the time range or choose a model') !== null
    ).toBe(special)
    expect(screen.queryByRole('button', { name: 'Retry' }) !== null).toBe(
      !special
    )
    expect(screen.queryByText('private response text')).not.toBeInTheDocument()
  }
)

it('switching administrators clears previous data and directory choices', async () => {
  const get = mockResult()
  renderPage()
  await screen.findByRole('table', { name: 'Current video task records' })
  get.mockResolvedValueOnce({
    data: { success: true, data: emptyVideoPayload },
  })
  await act(async () => {
    useAuthStore.getState().auth.setUser({
      id: 22,
      username: 'other',
      role: ROLE.ADMIN,
      permissions: { admin_permissions: { video_usage: { read: true } } },
    })
  })
  await screen.findByText('No recorded video tasks')
  await userEvent.click(screen.getByRole('combobox', { name: 'User' }))
  expect(await screen.findByRole('option', { name: 'All users' })).toBeVisible()
  expect(
    screen.queryByRole('option', { name: 'Alice (#7)' })
  ).not.toBeInTheDocument()
  await userEvent.keyboard('{Escape}')
  await userEvent.click(screen.getByRole('combobox', { name: 'Channel' }))
  expect(
    await screen.findByRole('option', { name: 'All channels' })
  ).toBeVisible()
  expect(
    screen.queryByRole('option', { name: 'H3 Metaso (#12)' })
  ).not.toBeInTheDocument()
})

it('losing administrator rights immediately hides operational data without a second request', async () => {
  const get = mockResult()
  renderPage()
  await screen.findByRole('table', { name: 'Current video task records' })
  await act(async () => {
    useAuthStore
      .getState()
      .auth.setUser({ id: 1, username: 'admin', role: ROLE.USER })
  })
  expect(screen.getByText('Access denied')).toBeVisible()
  expect(screen.queryByText('MiniMax-H3')).not.toBeInTheDocument()
  expect(get).toHaveBeenCalledOnce()
})

it('invalid dates block applying and a today preset is capped to the current time', async () => {
  const get = mockResult()
  renderPage()
  await screen.findByRole('table', { name: 'Current video task records' })
  const user = userEvent.setup()
  const period = screen.getByRole('group', { name: 'History period' })
  await user.click(within(period).getByRole('button'))
  fireEvent.change(screen.getByLabelText('Start Time'), {
    target: { value: '2026-01-01T00:00' },
  })
  fireEvent.change(screen.getByLabelText('End Time'), {
    target: { value: '2026-03-01T00:00' },
  })
  await user.click(screen.getByRole('button', { name: 'Confirm' }))
  expect(screen.getByRole('button', { name: 'Apply filters' })).toBeDisabled()
  expect(get).toHaveBeenCalledOnce()
  await user.click(within(period).getByRole('button'))
  await user.click(screen.getByRole('button', { name: 'Today' }))
  await user.click(screen.getByRole('button', { name: 'Apply filters' }))
  await waitFor(() => expect(get).toHaveBeenCalledTimes(2))
  expect(get.mock.lastCall?.[1]?.params.end_timestamp).toBeLessThanOrEqual(
    Math.floor(Date.now() / 1000)
  )
})

it('valid zero turnaround remains zero while its sample count includes zero as a subset', async () => {
  mockResult({
    ...videoPayload,
    summary: {
      ...videoPayload.summary,
      history: {
        ...videoPayload.summary.history,
        turnaround: {
          mean_seconds: 0,
          p50_seconds: 0,
          p95_seconds: 0,
          valid_samples: 1,
          missing_samples: 0,
          invalid_samples: 0,
          zero_samples: 1,
        },
      },
    },
  })
  renderPage()
  const summary = await screen.findByRole('region', {
    name: 'Video usage summary',
  })
  expect(within(summary).getAllByText('0 s')).toHaveLength(2)
  expect(
    screen.getByText('Turnaround samples: 1 valid / 0 missing / 0 invalid.')
  ).toBeVisible()
})

it('a malformed response cannot present an unknown source as a measured video duration', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: {
      success: true,
      data: {
        ...videoPayload,
        tasks: {
          ...videoPayload.tasks,
          items: [
            {
              ...videoPayload.tasks.items[0],
              reported_video_duration_source: 'unknown',
            },
          ],
        },
      },
    },
  })
  renderPage()
  expect(await screen.findByText('Failed to load video usage')).toBeVisible()
  expect(
    screen.queryByRole('table', { name: 'Current video task records' })
  ).not.toBeInTheDocument()
})

it('keyboard navigation opens the task tab and keeps its selected state accessible', async () => {
  mockResult()
  renderPage()
  await screen.findByRole('table', { name: 'Current video task records' })
  screen.getByRole('tab', { name: 'Current task records' }).focus()
  await userEvent.keyboard('{End}{Enter}')
  expect(screen.getByRole('tab', { name: 'Task details' })).toHaveAttribute(
    'aria-selected',
    'true'
  )
  expect(
    screen.getByRole('region', { name: 'Video task details' })
  ).toBeVisible()
})

it('narrow screens use existing mobile task cards while retaining missing timing and pagination', async () => {
  const matchMedia = window.matchMedia.bind(window)
  vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
    ...matchMedia(query),
    matches: query === '(max-width: 640px)',
  }))
  mockResult()
  renderPage()
  await screen.findByRole('table', { name: 'Current video task records' })
  await userEvent.click(screen.getByRole('tab', { name: 'Task details' }))
  expect(screen.queryByRole('table')).not.toBeInTheDocument()
  const details = screen.getByRole('region', { name: 'Video task details' })
  expect(within(details).getByText('Timing missing')).toBeVisible()
  expect(within(details).getByText('Desktop client declaration')).toBeVisible()
  expect(
    within(details).getByRole('button', { name: 'Go to next page' })
  ).toBeEnabled()
})

it('a prototype-like unknown task status renders a controlled unknown label', async () => {
  mockResult({
    ...videoPayload,
    tasks: {
      ...videoPayload.tasks,
      items: [{ ...videoPayload.tasks.items[0], status: 'constructor' }],
    },
  })
  renderPage()
  await screen.findByRole('table', { name: 'Current video task records' })
  await userEvent.click(screen.getByRole('tab', { name: 'Task details' }))
  const row = screen.getByRole('row', { name: /task-101/ })
  expect(within(row).getByText('Recorded unknown states')).toBeVisible()
})

it('channel and provider filters apply explicitly while recent windows preserve all applied filters', async () => {
  const get = mockResult()
  renderPage()
  await screen.findByRole('table', { name: 'Current video task records' })
  const user = userEvent.setup()
  for (const [field, option] of [
    ['User', 'Alice (#7)'],
    ['Model', 'MiniMax-H3'],
    ['Client source', 'Desktop client declaration'],
    ['Channel', 'H3 Metaso (#12)'],
    ['Provider (current settings)', 'Metaso'],
  ]) {
    await user.click(screen.getByRole('combobox', { name: field }))
    await user.click(await screen.findByRole('option', { name: option }))
  }
  expect(get).toHaveBeenCalledOnce()
  await user.click(screen.getByRole('button', { name: 'Apply filters' }))
  await waitFor(() => expect(get).toHaveBeenCalledTimes(2))
  const applied = get.mock.lastCall?.[1]?.params
  expect(applied).toEqual(
    expect.objectContaining({
      user_id: 7,
      model: 'MiniMax-H3',
      client_source: 'desktop',
      channel_id: 12,
      provider: 'metaso',
    })
  )
  await user.click(screen.getByRole('tab', { name: 'Video trends' }))
  await user.click(
    await screen.findByRole('button', { name: 'Last 5 minutes' })
  )
  await waitFor(() => expect(get).toHaveBeenCalledTimes(3))
  const recent = get.mock.lastCall?.[1]?.params
  expect(recent).toEqual(
    expect.objectContaining({
      ...applied,
      start_timestamp: expect.any(Number),
      end_timestamp: expect.any(Number),
      p: 1,
    })
  )
  expect(recent.end_timestamp - recent.start_timestamp).toBe(300)
  await user.click(await screen.findByRole('button', { name: 'Last 1 hour' }))
  await waitFor(() => expect(get).toHaveBeenCalledTimes(4))
  const hour = get.mock.lastCall?.[1]?.params
  expect(hour.end_timestamp - hour.start_timestamp).toBe(3600)
  expect(hour).toEqual(
    expect.objectContaining({
      channel_id: 12,
      provider: 'metaso',
      model: 'MiniMax-H3',
      user_id: 7,
      client_source: 'desktop',
    })
  )
})

it('a missing channel remains an explicit zero-id filter and channel rows use real provider attribution', async () => {
  const get = mockResult()
  renderPage()
  await screen.findByRole('table', { name: 'Current video task records' })
  const user = userEvent.setup()
  await user.click(screen.getByRole('tab', { name: 'Channel breakdown' }))
  const table = screen.getByRole('table', { name: 'Video usage by channel' })
  expect(
    within(table).getByRole('row', { name: /H3 Metaso/ })
  ).toHaveTextContent('Metaso')
  expect(within(table).getByRole('row', { name: /H3 DMX/ })).toHaveTextContent(
    'DMX'
  )
  expect(
    within(table).getByRole('row', { name: /H3 Blackrx/ })
  ).toHaveTextContent('Blackrx')
  expect(
    within(table).getByRole('row', { name: /Unknown channel/ })
  ).toHaveTextContent('Unknown')
  await user.click(screen.getByRole('combobox', { name: 'Channel' }))
  await user.click(
    await screen.findByRole('option', { name: 'Unknown channel' })
  )
  await user.click(screen.getByRole('button', { name: 'Apply filters' }))
  await waitFor(() => expect(get.mock.lastCall?.[1]?.params.channel_id).toBe(0))
})
