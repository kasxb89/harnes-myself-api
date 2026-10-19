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
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { Route as usageLogsRoute } from '@/routes/_authenticated/usage-logs/$section'
import { useAuthStore } from '@/stores/auth-store'

import { usageLogSchema } from '../../data/schema'
import { fetchLogsByCategory } from '../../lib/utils'
import { UsageLogsProvider } from '../usage-logs-provider'
import { UsageLogsTable } from '../usage-logs-table'

// Chart rendering is outside this route and log-table contract.
vi.mock('@visactor/react-vchart', () => ({ VChart: () => null }))

const reason = `Upstream rejected reasoning_content. ${'Detailed diagnostic context. '.repeat(80)}`
const record = usageLogSchema.parse({
  id: 1,
  user_id: 42,
  username: 'alice',
  created_at: 1792380605,
  type: 5,
  content: reason,
  model_name: 'deepseek-flash',
  channel: 16,
  channel_name: 'Production',
  request_id: 'req-diagnostic-123',
  other: JSON.stringify({
    status_code: 400,
    error_code: 'invalid_request_error',
  }),
})

async function renderErrors(empty = false, mobile = false) {
  if (mobile) {
    const matchMedia = window.matchMedia
    vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
      ...matchMedia(query),
      matches:
        query === '(max-width: 640px)' ||
        query.includes('prefers-reduced-motion'),
    }))
  }
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'admin', role: ROLE.ADMIN })
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data: url.startsWith('/api/log?')
        ? { items: empty ? [] : [record], total: empty ? 0 : 1 }
        : [],
    },
  }))
  const root = createRootRoute()
  const auth = createRoute({ getParentRoute: () => root, id: '_authenticated' })
  const logs = createRoute({
    getParentRoute: () => auth,
    path: '/usage-logs/$section',
    validateSearch: (search: Record<string, unknown>) => search,
    component: () => (
      <UsageLogsProvider>
        <UsageLogsTable logCategory='common' errorOnly />
      </UsageLogsProvider>
    ),
  })
  const router = createRouter({
    routeTree: root.addChildren([auth.addChildren([logs])]),
    history: createMemoryHistory({
      initialEntries: ['/usage-logs/errors?type=2'],
    }),
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  if (mobile) {
    await screen.findByRole('button', { name: 'Filter' })
  } else {
    await screen.findByPlaceholderText('Model Name')
  }
  return { router, get }
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})

it('shows diagnostics with second precision and timezone while enforcing error-only requests', async () => {
  const { get } = await renderErrors()
  await screen.findByText('deepseek-flash')
  const calls = get.mock.calls.map(([url]) => String(url))
  const logCall = calls.find((url) => url.startsWith('/api/log?')) ?? ''
  expect(new URL(logCall, 'https://test.local').searchParams.get('type')).toBe(
    '5'
  )
  expect(
    screen.queryByRole('combobox', { name: 'Type' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('columnheader', { name: 'Cost' })
  ).not.toBeInTheDocument()
  expect(
    screen.getByText(/\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} [+-]\d{2}:\d{2}/)
  ).toBeVisible()
  expect(screen.getByText('400')).toBeVisible()
  expect(screen.getByText('req-diagnostic-123')).toBeVisible()
  expect(screen.getByText('alice')).toBeVisible()
  await userEvent.click(screen.getByTitle('Click to view full details'))
  expect(
    within(await screen.findByRole('dialog')).getByText(reason.trim())
  ).toBeVisible()
  expect(
    calls.some(
      (url) => url.includes('/stat?') || url.includes('/subscription/')
    )
  ).toBe(false)
})

it('searches and resets user and model filters without leaving the error page', async () => {
  const { router } = await renderErrors()
  await userEvent.type(screen.getByPlaceholderText('Username'), 'alice')
  await userEvent.type(
    screen.getByPlaceholderText('Model Name'),
    'deepseek-flash'
  )
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(router.state.location.search).toMatchObject({
      username: 'alice',
      model: 'deepseek-flash',
      type: ['5'],
    })
  )
  expect(router.state.location.pathname).toBe('/usage-logs/errors')
  await userEvent.click(screen.getByRole('button', { name: 'Reset' }))
  await waitFor(() =>
    expect(router.state.location.search).toMatchObject({ type: ['5'] })
  )
  expect(router.state.location.pathname).toBe('/usage-logs/errors')
  expect(screen.getByPlaceholderText('Username')).toHaveValue('')
})

it('shows the error-specific empty state when no failures match', async () => {
  await renderErrors(true)
  expect(await screen.findByText('No API errors found')).toBeVisible()
})

it('never reads administrator error records through a non-admin scope', async () => {
  const get = vi.spyOn(api, 'get')
  await expect(
    fetchLogsByCategory({
      logCategory: 'common',
      isAdmin: false,
      errorOnly: true,
      page: 1,
      pageSize: 20,
      searchParams: {},
      columnFilters: [],
    })
  ).rejects.toThrow()
  expect(get).not.toHaveBeenCalled()
})

it('redirects non-admin visitors away from the diagnostics route', async () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 42, username: 'alice', role: ROLE.USER })
  await expect(async () =>
    usageLogsRoute.options.beforeLoad?.({
      params: { section: 'errors' },
      search: {},
    } as never)
  ).rejects.toMatchObject({ options: { to: '/403' } })
})

it('keeps timestamp, status and request identifiers readable on the mobile error card', async () => {
  await renderErrors(false, true)
  await screen.findByText('deepseek-flash')
  expect(
    screen.getByRole('button', { name: /^Time: \d{4}.*[+-]\d{2}:\d{2}$/ })
  ).toBeVisible()
  expect(screen.getByText('HTTP Status: 400')).toBeVisible()
  expect(screen.getByText('Request ID: req-diagnostic-123')).toBeVisible()
  await userEvent.click(screen.getByTitle('Click to view full details'))
  expect(
    within(await screen.findByRole('dialog')).getByText(reason.trim())
  ).toBeVisible()
})
