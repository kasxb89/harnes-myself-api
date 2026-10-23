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
  createRootRoute,
  createRoute,
  createRouter,
  createMemoryHistory,
  RouterProvider,
} from '@tanstack/react-router'
import {
  act,
  render,
  renderHook,
  screen,
  waitFor,
} from '@testing-library/react'
import { beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { CommonLogsStats } from '../common-logs-stats'
import { UsageLogsProvider, useLogsViewScope } from '../usage-logs-provider'

function setPermissions(read: boolean) {
  useAuthStore.getState().auth.setUser({
    id: 2,
    username: 'operator',
    role: ROLE.ADMIN,
    permissions: { admin_permissions: { diagnostics: { read } } },
  })
}
beforeEach(() => setPermissions(false))

test('an administrator without diagnostics permission is restricted to self even when all scope is requested', () => {
  const hook = renderHook(() => useLogsViewScope(), {
    wrapper: UsageLogsProvider,
  })
  expect(hook.result.current.canManageScope).toBe(false)
  expect(hook.result.current.viewScope).toBe('self')
  expect(hook.result.current.isAdminView).toBe(false)
  act(() => hook.result.current.setViewScope('all'))
  expect(hook.result.current.viewAccess).toBe('self')
})

test('revoking diagnostics removes all-scope access synchronously and resolves the effective view to self', () => {
  setPermissions(true)
  const hook = renderHook(() => useLogsViewScope(), {
    wrapper: UsageLogsProvider,
  })
  expect(hook.result.current.viewAccess).toBe('admin')
  act(() => setPermissions(false))
  expect(hook.result.current.viewAccess).toBe('self')
  expect(hook.result.current.viewScope).toBe('self')
})

test('revoking diagnostics switches the real stats query to self without exposing previous all-scope placeholder totals', async () => {
  setPermissions(true)
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (String(url).startsWith('/api/log/self/stat')) {
      return new Promise(() => undefined)
    }
    return { data: { success: true, data: { quota: 0, rpm: 987654, tpm: 0 } } }
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  const root = createRootRoute()
  const auth = createRoute({ getParentRoute: () => root, id: '_authenticated' })
  const logs = createRoute({
    getParentRoute: () => auth,
    path: 'usage-logs/$section',
    component: () => (
      <UsageLogsProvider>
        <CommonLogsStats />
      </UsageLogsProvider>
    ),
  })
  const router = createRouter({
    routeTree: root.addChildren([auth.addChildren([logs])]),
    history: createMemoryHistory({ initialEntries: ['/usage-logs/common'] }),
  })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  expect(await screen.findByText('987654')).toBeVisible()
  act(() => setPermissions(false))
  await waitFor(() =>
    expect(
      get.mock.calls.some(([url]) =>
        String(url).startsWith('/api/log/self/stat')
      )
    ).toBe(true)
  )
  expect(screen.queryByText('987654')).not.toBeInTheDocument()
  client.clear()
})
