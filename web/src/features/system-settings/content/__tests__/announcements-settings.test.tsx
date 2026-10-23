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
import { act, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { Route as ContentIndexRoute } from '@/routes/_authenticated/system-settings/content/index'
import { Route as SystemSettingsRoute } from '@/routes/_authenticated/system-settings/route'
import { useAuthStore } from '@/stores/auth-store'

import {
  ANNOUNCEMENTS_QUERY_KEY,
  type AnnouncementsSettings,
} from '../announcements-api'
import { AnnouncementsSettingsSection } from '../announcements-settings-section'
import { ContentSettings } from '../index'

let stored: AnnouncementsSettings
function renderSection(content = <AnnouncementsSettingsSection />) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  return {
    client,
    ...render(
      <QueryClientProvider client={client}>{content}</QueryClientProvider>
    ),
  }
}
function setAdmin(write = true) {
  useAuthStore.getState().auth.setUser({
    id: 2,
    username: 'operator',
    role: ROLE.ADMIN,
    permissions: {
      admin_permissions: { announcements: { read: true, write } },
    },
  })
}
beforeEach(() => {
  stored = {
    'console_setting.announcements': '[]',
    'console_setting.announcements_enabled': 'true',
  }
  setAdmin()
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url !== '/api/announcements') {
      throw new Error('unexpected broad settings request')
    }
    return { data: { success: true, data: { ...stored } } }
  })
  vi.spyOn(api, 'put').mockImplementation(async (url, data) => {
    if (url !== '/api/announcements') {
      throw new Error('unexpected generic settings write')
    }
    stored = data as AnnouncementsSettings
    return { data: { success: true, data: { ...stored } } }
  })
})

test('the content route loads announcements through the narrow API without requesting all system options', async () => {
  const root = createRootRoute()
  const authenticated = createRoute({
    getParentRoute: () => root,
    id: '_authenticated',
  })
  const settings = createRoute({
    getParentRoute: () => authenticated,
    path: 'system-settings',
  })
  const content = createRoute({
    getParentRoute: () => settings,
    path: 'content/$section',
    component: ContentSettings,
  })
  const router = createRouter({
    routeTree: root.addChildren([
      authenticated.addChildren([settings.addChildren([content])]),
    ]),
    history: createMemoryHistory({
      initialEntries: ['/system-settings/content/announcements'],
    }),
  })
  renderSection(<RouterProvider router={router} />)
  expect(
    await screen.findByRole('button', { name: 'Add Announcement' })
  ).toBeEnabled()
  expect(api.get).toHaveBeenCalledWith('/api/announcements', expect.anything())
  expect(
    vi.mocked(api.get).mock.calls.every(([url]) => url === '/api/announcements')
  ).toBe(true)
})

test('a read-only announcement operator cannot edit, add, delete, toggle or save', async () => {
  setAdmin(false)
  renderSection()
  expect(
    await screen.findByRole('button', { name: 'Add Announcement' })
  ).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Save Settings' })).toBeDisabled()
  expect(screen.getByRole('switch', { name: 'Enabled' })).toHaveAttribute(
    'aria-disabled',
    'true'
  )
  expect(api.put).not.toHaveBeenCalled()
})

test('a failed atomic announcement save keeps the enabled draft and retries both exact keys together', async () => {
  const user = userEvent.setup()
  vi.mocked(api.put).mockRejectedValueOnce(new Error('temporary failure'))
  const view = renderSection()
  const toggle = await screen.findByRole('switch', { name: 'Enabled' })
  await user.click(toggle)
  expect(toggle).not.toBeChecked()
  expect(api.put).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Save Settings' }))
  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  act(() => view.client.setQueryData(ANNOUNCEMENTS_QUERY_KEY, { ...stored }))
  expect(toggle).not.toBeChecked()
  expect(screen.getByRole('button', { name: 'Save Settings' })).toBeEnabled()
  await user.click(screen.getByRole('button', { name: 'Save Settings' }))
  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(2))
  expect(api.put).toHaveBeenLastCalledWith('/api/announcements', {
    'console_setting.announcements': '[]',
    'console_setting.announcements_enabled': 'false',
  })
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save Settings' })).toBeDisabled()
  )
})

test('revoking announcement access removes cached content and every editing action', async () => {
  renderSection()
  expect(
    await screen.findByRole('button', { name: 'Add Announcement' })
  ).toBeVisible()
  act(() =>
    useAuthStore.getState().auth.setUser({
      id: 2,
      username: 'operator',
      role: ROLE.ADMIN,
      permissions: { admin_permissions: {} },
    })
  )
  expect(screen.getByText('Access denied')).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Add Announcement' })
  ).not.toBeInTheDocument()
})

test('a delegated content reader can enter its section but the parent guard blocks every unrelated system settings path', () => {
  const beforeLoad = SystemSettingsRoute.options
    .beforeLoad as unknown as (context: {
    location: { pathname: string }
  }) => void
  expect(() =>
    beforeLoad({
      location: { pathname: '/system-settings/content/announcements' },
    })
  ).not.toThrow()
  expect(() =>
    beforeLoad({
      location: { pathname: '/system-settings/content/customer-service' },
    })
  ).toThrow()
  expect(() =>
    beforeLoad({ location: { pathname: '/system-settings/auth/oauth' } })
  ).toThrow()
  expect(api.get).not.toHaveBeenCalled()
})

test('legacy announcement section query links redirect only to the granted content section', () => {
  const beforeLoad = ContentIndexRoute.options
    .beforeLoad as unknown as (context: {
    location: { searchStr: string }
  }) => void
  expect.assertions(1)
  try {
    beforeLoad({ location: { searchStr: '?section=announcements' } })
  } catch (redirect) {
    expect(redirect).toMatchObject({
      options: {
        to: '/system-settings/content/$section',
        params: { section: 'announcements' },
      },
    })
  }
})

test('a root user retains legacy query links to content sections outside delegated customer service and announcements', () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'root', role: ROLE.SUPER_ADMIN })
  const beforeLoad = ContentIndexRoute.options
    .beforeLoad as unknown as (context: {
    location: { searchStr: string }
  }) => void
  expect.assertions(1)
  try {
    beforeLoad({ location: { searchStr: '?section=faq' } })
  } catch (redirect) {
    expect(redirect).toMatchObject({
      options: {
        to: '/system-settings/content/$section',
        params: { section: 'faq' },
      },
    })
  }
})
