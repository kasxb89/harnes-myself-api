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
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import {
  act,
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { mediaPayload } from '@/features/media-operations/__tests__/fixtures'
import type { AdminPermissionMatrix } from '@/lib/admin-permissions'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { OperationsScreen, OperationsScreenPage } from '..'
import { operationsSearchSchema } from '../policy'

const originalAdapter = api.defaults.adapter
const clients: QueryClient[] = []
const totals = {
  requests: 2,
  revenue_cny: 5,
  known_cost_revenue_cny: 5,
  upstream_cost_cny: 1,
  gross_profit_cny: 4,
  profit_margin: 0.8,
  known_cost_requests: 2,
  unknown_cost_requests: 0,
  known_cost_events: 2,
  unknown_cost_events: 0,
  cost_coverage: 1,
  pending_requests: 0,
  pending_revenue_cny: 0,
  refund_cny: 0,
}
const concurrency = {
  schema_version: 1,
  revision: 'fixture',
  defaults: { video: 2, image: 4 },
  pool_overrides: [],
  pools: [],
}
const calls: string[] = []
function setPermissions(permissions: AdminPermissionMatrix, id = 7) {
  useAuthStore.getState().auth.setUser({
    id,
    username: 'operator',
    role: 10,
    permissions: { admin_permissions: permissions },
  })
}
async function setup(requestedTab?: string, routePath?: string) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  const root = createRootRoute({
    component: routePath
      ? undefined
      : () => <OperationsScreen requestedTab={requestedTab} />,
  })
  const routeTree = routePath
    ? root.addChildren([
        createRoute({
          getParentRoute: () => root,
          path: '/operations-screen',
          validateSearch: operationsSearchSchema,
          component: OperationsScreenPage,
        }),
      ])
    : root
  const router = createRouter({
    routeTree,
    history: createMemoryHistory({ initialEntries: [routePath ?? '/'] }),
  })
  await router.load()
  const view = render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  await screen.findByRole('tablist', { name: 'Operations screen sections' })
  return { ...view, client, router }
}
async function expandDetails() {
  const trigger = await screen.findByRole('button', { name: 'Details' })
  await waitFor(() => expect(trigger).toBeEnabled())
  if (trigger.getAttribute('aria-expanded') === 'false') {
    await userEvent.click(trigger)
  }
}
beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
  calls.length = 0
  setPermissions({
    model_profits: { read: true },
    media_operations: { read: true },
    diagnostics: { read: true },
    user_activity: { read: true },
  })
  api.defaults.adapter = async (config) => {
    expect(config.method).toBe('get')
    expect(config.disableDuplicate).toBe(true)
    expect(config.signal).toBeDefined()
    calls.push(config.url ?? '')
    let data: unknown = concurrency
    if (config.url === '/api/model-profit/') {
      data = {
        rows: [{ ...totals, model_name: 'fixture-model' }],
        summary: totals,
        timezone: 'Asia/Shanghai',
        currency: 'CNY',
        ...config.params,
      }
    }
    if (config.url === '/api/media-operations') data = mediaPayload
    if (config.url === '/api/log/') data = { items: [], total: 0 }
    if (config.url === '/api/log/stat') data = { quota: 0, rpm: 0, tpm: 0 }
    if (config.url === '/api/client-activity/sessions') {
      data = { items: [], total: 0 }
    }
    if (config.url === '/api/client-activity/summary') {
      data = {
        server_time: 1792810800,
        heartbeat_interval_seconds: 30,
        online_timeout_seconds: 90,
        online_users: 0,
        foreground_users: 0,
        runtime_sessions: 0,
        started_sessions: 0,
        active_users: 0,
        online_seconds: 0,
        foreground_seconds: 0,
        model_calls: 0,
        model_errors: 0,
      }
    }
    return {
      data: { success: true, data },
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    }
  }
})
afterEach(() => {
  cleanup()
  clients.splice(0).forEach((client) => client.clear())
  api.defaults.adapter = originalAdapter
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
})
it('puts the ink observation core and two chart stations before collapsed details and expands records without another request', async () => {
  await setup()
  const deck = await screen.findByRole('region', { name: 'Operations screen' })
  expect(deck).toHaveClass('ops-hud-deck')
  expect(deck.querySelector('.ops-hud-core-slot')).not.toBeNull()
  expect(deck.querySelectorAll('.ops-hud-flank')).toHaveLength(2)
  expect(screen.getByRole('button', { name: 'Details' })).toHaveAttribute(
    'aria-expanded',
    'false'
  )
  expect(
    screen.queryByRole('table', { name: 'Profit by model' })
  ).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Details' }))
  expect(
    await screen.findByRole('table', { name: 'Profit by model' })
  ).toBeVisible()
  expect(calls).toEqual(['/api/model-profit/'])
})
it('shows one screen title in the page without an extra page-layout heading', async () => {
  setPermissions({ media_operations: { read: true } })
  await setup()
  expect(
    screen.getAllByRole('heading', { name: 'Operations screen' })
  ).toHaveLength(1)
  expect(
    screen.getByRole('heading', { name: 'Operations screen', level: 1 })
  ).toBeVisible()
})
it('offers separate image and video tabs, wraps the mobile strip, and loads only the selected read-only report', async () => {
  await setup()
  const tabs = screen.getByRole('tablist', {
    name: 'Operations screen sections',
  })
  expect(within(tabs).getAllByRole('tab')).toHaveLength(7)
  for (const label of [
    'Profit',
    'Concurrency',
    'Call usage',
    'API errors',
    'Image',
    'Video',
    'User activity',
  ]) {
    expect(within(tabs).getByRole('tab', { name: label })).toHaveAccessibleName(
      label
    )
  }
  expect(tabs).toHaveClass('flex-wrap', 'w-full')
  await expandDetails()
  await screen.findByRole('table', { name: 'Profit by model' })
  expect(calls).toEqual(['/api/model-profit/'])
  expect(screen.getAllByText('Pending confirmation').length).toBeGreaterThan(0)
  await userEvent.click(within(tabs).getByRole('tab', { name: 'Concurrency' }))
  await screen.findByText('No registered image or video account pools')
  expect(calls).toEqual(['/api/model-profit/', '/api/media-concurrency'])
  expect(
    screen.queryByRole('table', { name: 'Profit by model' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Save media concurrency' })
  ).not.toBeInTheDocument()
  await userEvent.click(within(tabs).getByRole('tab', { name: 'Call usage' }))
  await screen.findByRole('table', { name: 'Recent consumption records' })
  expect(calls.slice(-2)).toEqual(['/api/log/', '/api/log/stat'])
})
it('falls back from an unauthorized initial tab and cancels and clears the sample immediately on grant revocation', async () => {
  setPermissions({ media_operations: { read: true } })
  const view = await setup('errors')
  await expandDetails()
  await screen.findByText('No registered image or video account pools')
  expect(screen.getByRole('tab', { name: 'Concurrency' })).toHaveAttribute(
    'aria-selected',
    'true'
  )
  expect(
    screen.queryByRole('tab', { name: 'API errors' })
  ).not.toBeInTheDocument()
  expect(calls).toEqual(['/api/media-concurrency'])
  act(() => setPermissions({ diagnostics: { read: true } }))
  await expandDetails()
  await screen.findByRole('table', { name: 'Recent API error records' })
  expect(
    screen.queryByText('No registered image or video account pools')
  ).not.toBeInTheDocument()
  expect(
    view.client
      .getQueryCache()
      .getAll()
      .every((query) => String(query.queryKey[1]).includes('diagnostics'))
  ).toBe(true)
  act(() => setPermissions({}))
  await screen.findByText('Access denied')
  await waitFor(() =>
    expect(view.client.getQueryCache().getAll()).toHaveLength(0)
  )
})
it('marks the retained ink sample stale and stops its animation after a failed refresh', async () => {
  setPermissions({ media_operations: { read: true } })
  await setup()
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Details' })).toBeEnabled()
  )
  const core = screen.getByRole('region', { name: 'Occupied admission slots' })
  expect(core).toHaveAttribute('data-stale', 'false')
  api.defaults.adapter = async (config) => ({
    data: { success: false, message: 'Fixture refresh unavailable' },
    status: 200,
    statusText: 'OK',
    headers: {},
    config,
  })
  await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Fixture refresh unavailable'
  )
  expect(core).toHaveAttribute('data-stale', 'true')
  expect(core).toHaveAttribute('data-paused', 'true')
  expect(within(core).getByText('Stale sample')).toBeVisible()
})
it('exposes pause and refresh state through accessible controls', async () => {
  setPermissions({ media_operations: { read: true } })
  await setup()
  await expandDetails()
  await screen.findByText('No registered image or video account pools')
  const shell = screen
    .getByRole('heading', { name: 'Operations screen', level: 1 })
    .closest('.operations-screen')
  expect(shell).toHaveAttribute('data-paused', 'false')
  expect(shell?.querySelector('.ops-scanline')).toHaveAttribute(
    'aria-hidden',
    'true'
  )
  await userEvent.click(screen.getByRole('button', { name: 'Pause' }))
  expect(shell).toHaveAttribute('data-paused', 'true')
  expect(
    screen.getByRole('region', { name: 'Occupied admission slots' })
  ).toHaveAttribute('data-paused', 'true')
  expect(screen.getByRole('button', { name: 'Resume' })).toHaveAttribute(
    'aria-pressed',
    'true'
  )
  expect(
    within(screen.getByRole('status', { name: 'Refresh status' })).getByText(
      'Paused'
    )
  ).toBeVisible()
  await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
  await waitFor(() => expect(calls).toHaveLength(2))
  expect(screen.getByRole('button', { name: 'Resume' })).toHaveAttribute(
    'aria-pressed',
    'true'
  )
  screen.getByRole('tab', { name: 'Concurrency' }).focus()
  await userEvent.keyboard('{ArrowRight}{Enter}')
  expect(screen.getByRole('tab', { name: 'Image' })).toHaveAttribute(
    'aria-selected',
    'true'
  )
  await userEvent.click(screen.getByRole('button', { name: 'Refresh' }))
  expect(
    await screen.findByRole('table', { name: 'Current task records' })
  ).toBeVisible()
})

it('keeps the selected profit route across fullscreen entry and exit after starting on activity, with the picker inside fullscreen', async () => {
  const descriptors = [
    'fullscreenEnabled',
    'fullscreenElement',
    'exitFullscreen',
  ].map((key) => [key, Object.getOwnPropertyDescriptor(document, key)] as const)
  const enterDescriptor = Object.getOwnPropertyDescriptor(
    HTMLElement.prototype,
    'requestFullscreen'
  )
  Object.defineProperty(document, 'fullscreenEnabled', {
    configurable: true,
    value: true,
  })
  Object.defineProperty(document, 'fullscreenElement', {
    configurable: true,
    writable: true,
    value: null,
  })
  Object.defineProperty(HTMLElement.prototype, 'requestFullscreen', {
    configurable: true,
    value(this: HTMLElement) {
      Object.defineProperty(document, 'fullscreenElement', {
        configurable: true,
        writable: true,
        value: this,
      })
      document.dispatchEvent(new Event('fullscreenchange'))
      return Promise.resolve()
    },
  })
  Object.defineProperty(document, 'exitFullscreen', {
    configurable: true,
    value: () => {
      Object.defineProperty(document, 'fullscreenElement', {
        configurable: true,
        writable: true,
        value: null,
      })
      document.dispatchEvent(new Event('fullscreenchange'))
      return Promise.resolve()
    },
  })
  try {
    const { router } = await setup(undefined, '/operations-screen?tab=activity')
    await expandDetails()
    await screen.findByRole('table', { name: 'Current client processes' })
    await userEvent.click(screen.getByRole('tab', { name: 'Profit' }))
    await screen.findByRole('table', { name: 'Profit by model' })
    expect(router.state.location.search).toEqual({ tab: 'profit' })
    expect(
      screen.getAllByRole('heading', { name: 'Operations screen' })
    ).toHaveLength(1)
    await userEvent.click(
      screen.getByRole('button', { name: 'Enter fullscreen' })
    )
    expect(
      screen.getByRole('button', { name: 'Exit fullscreen' })
    ).toHaveAttribute('aria-pressed', 'true')
    expect(router.state.location.search).toEqual({ tab: 'profit' })
    expect(
      screen.getAllByRole('heading', { name: 'Operations screen' })
    ).toHaveLength(1)
    expect(document.fullscreenElement).toContainElement(
      screen.getByRole('heading', { name: 'Operations screen', level: 1 })
    )
    await userEvent.click(
      screen.getByRole('combobox', { name: 'Observation window' })
    )
    expect(document.fullscreenElement).toContainElement(
      await screen.findByRole('listbox')
    )
    await userEvent.keyboard('{Escape}')
    await userEvent.click(screen.getByRole('combobox', { name: 'Model' }))
    expect(document.fullscreenElement).toContainElement(
      await screen.findByRole('listbox')
    )
    await userEvent.keyboard('{Escape}')
    await userEvent.click(
      screen.getByRole('button', { name: /\d{4}-\d{2}-.*~/ })
    )
    expect(document.fullscreenElement).toContainElement(
      screen.getByRole('textbox', { name: 'Start Time' })
    )
    await userEvent.keyboard('{Escape}')
    await userEvent.click(
      screen.getByRole('button', { name: 'Exit fullscreen' })
    )
    expect(
      screen.getByRole('button', { name: 'Enter fullscreen' })
    ).toHaveAttribute('aria-pressed', 'false')
    expect(router.state.location.search).toEqual({ tab: 'profit' })
    expect(router.history.location.href).toBe('/operations-screen?tab=profit')
    expect(screen.getByRole('tab', { name: 'Profit' })).toHaveAttribute(
      'aria-selected',
      'true'
    )
    expect(
      screen.getAllByRole('heading', { name: 'Operations screen' })
    ).toHaveLength(1)
  } finally {
    for (const [key, descriptor] of descriptors) {
      if (descriptor) Object.defineProperty(document, key, descriptor)
      else Reflect.deleteProperty(document, key)
    }
    if (enterDescriptor) {
      Object.defineProperty(
        HTMLElement.prototype,
        'requestFullscreen',
        enterDescriptor
      )
    } else Reflect.deleteProperty(HTMLElement.prototype, 'requestFullscreen')
  }
})
it('shows a failed first observation without presenting a successful zero sample', async () => {
  setPermissions({ diagnostics: { read: true } })
  api.defaults.adapter = async (config) => ({
    data: { success: false, message: 'Fixture read unavailable' },
    status: 200,
    statusText: 'OK',
    headers: {},
    config,
  })
  await setup()
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Fixture read unavailable'
  )
  expect(
    within(screen.getByRole('status', { name: 'Refresh status' })).getByText(
      'Refresh failed'
    )
  ).toBeVisible()
  expect(screen.queryByRole('table')).not.toBeInTheDocument()
  expect(
    screen.queryByText('Consumption records in range')
  ).not.toBeInTheDocument()
})

it('lets an operator paste a model and type or choose times, and applies them only after valid confirmation', async () => {
  const requests: Record<string, unknown>[] = []
  const adapter = api.defaults.adapter as (
    config: import('axios').InternalAxiosRequestConfig
  ) => Promise<import('axios').AxiosResponse>
  api.defaults.adapter = async (config) => {
    requests.push(config.params)
    return adapter(config)
  }
  await setup('errors')
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Apply filters' })).toBeEnabled()
  )
  const model = screen.getByRole('combobox', { name: 'Model' })
  await userEvent.click(model)
  await userEvent.paste('deepseek-flash')
  expect(requests).toHaveLength(1)
  await userEvent.click(screen.getByRole('button', { name: /\d{4}-\d{2}-.*~/ }))
  const start = screen.getByRole('textbox', { name: 'Start Time' })
  const end = screen.getByRole('textbox', { name: 'End Time' })
  await userEvent.clear(start)
  await userEvent.type(start, '2026-02-30 00:00:00')
  expect(start).toHaveAttribute('aria-invalid', 'true')
  expect(screen.getByRole('button', { name: 'Confirm' })).toBeDisabled()
  expect(requests).toHaveLength(1)
  await userEvent.clear(start)
  await userEvent.type(start, '2026-10-20 00:00:00')
  await userEvent.clear(end)
  await userEvent.type(end, '2026-10-20 01:00:00')
  expect(
    screen.getByRole('button', { name: 'Choose start date' })
  ).toBeVisible()
  await userEvent.click(screen.getByRole('button', { name: 'Confirm' }))
  expect(requests).toHaveLength(1)
  await userEvent.click(screen.getByRole('button', { name: 'Apply filters' }))
  await waitFor(() => expect(requests).toHaveLength(2))
  expect(requests[1]).toMatchObject({
    model_name: 'deepseek-flash',
    start_timestamp: new Date(2026, 9, 20, 0, 0).getTime() / 1000,
    end_timestamp: new Date(2026, 9, 20, 1, 0).getTime() / 1000,
  })
})

it('disables unsupported model and history controls for current concurrency without sending misleading filters', async () => {
  await setup('concurrency')
  expect(screen.getByRole('combobox', { name: 'Model' })).toBeDisabled()
  expect(
    screen.getByRole('combobox', { name: 'Observation window' })
  ).toBeDisabled()
  expect(
    screen.getByText(
      'Concurrency is a current global snapshot. Model and historical time filters do not apply.'
    )
  ).toBeVisible()
  expect(screen.getByRole('button', { name: 'Apply filters' })).toBeDisabled()
  expect(calls).toEqual(['/api/media-concurrency'])
})

it('disables user-activity model filtering and keeps its historical time picker available with the global-presence explanation', async () => {
  await setup('activity')
  expect(screen.getByRole('combobox', { name: 'Model' })).toBeDisabled()
  expect(
    screen.getByRole('combobox', { name: 'Observation window' })
  ).toBeEnabled()
  expect(
    screen.getByText(
      'Model filtering is unavailable for user activity. Current presence stays global; the selected time filters historical activity only.'
    )
  ).toBeVisible()
  expect(screen.getByRole('button', { name: /\d{4}-\d{2}-.*~/ })).toBeVisible()
})
