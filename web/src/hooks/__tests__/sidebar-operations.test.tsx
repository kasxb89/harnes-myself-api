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
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from '@testing-library/react'
import { afterAll, afterEach, beforeAll, expect, it } from 'vitest'

import { SearchProvider } from '@/context/search-provider'
import type { AdminPermissionMatrix } from '@/lib/admin-permissions'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { useSidebarView } from '../use-sidebar-view'

const clients: QueryClient[] = []
const animationsDescriptor = Object.getOwnPropertyDescriptor(
  HTMLElement.prototype,
  'getAnimations'
)
beforeAll(() => {
  Object.defineProperty(HTMLElement.prototype, 'getAnimations', {
    configurable: true,
    value: () => [],
  })
})
afterAll(() => {
  if (animationsDescriptor) {
    Object.defineProperty(
      HTMLElement.prototype,
      'getAnimations',
      animationsDescriptor
    )
  } else {
    Reflect.deleteProperty(HTMLElement.prototype, 'getAnimations')
  }
})
function NavigationProbe() {
  const { navGroups } = useSidebarView()
  return (
    <SearchProvider>
      <nav aria-label='Root navigation'>
        {navGroups.map((group) => (
          <section key={group.id} aria-label={group.title}>
            <h2>{group.title}</h2>
            {group.items.map((item) => (
              <div key={item.title}>
                <h3>{item.title}</h3>
                {(
                  item.items ??
                  (item.type === 'chat-presets' ? (item.links ?? []) : [item])
                ).map((child) =>
                  child.url ? (
                    <a key={child.url} href={child.url}>
                      {child.title}
                    </a>
                  ) : null
                )}
              </div>
            ))}
          </section>
        ))}
      </nav>
    </SearchProvider>
  )
}
async function setup(
  role: number = ROLE.SUPER_ADMIN,
  adminConfig?: object,
  permissions?: AdminPermissionMatrix
) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  })
  clients.push(client)
  client.setQueryData(['status'], {
    SidebarModulesAdmin: adminConfig ? JSON.stringify(adminConfig) : '',
  })
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'test',
    role,
    permissions: { admin_permissions: permissions },
  })
  const root = createRootRoute({ component: NavigationProbe })
  const router = createRouter({
    routeTree: root,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await router.load()
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  await screen.findByRole('navigation', { name: 'Root navigation' })
}
afterEach(() => {
  cleanup()
  clients.splice(0).forEach((client) => client.clear())
  useAuthStore.getState().auth.reset()
})

it('groups root navigation into nine business entries and lists each destination once', async () => {
  await setup()
  expect(
    within(screen.getByRole('navigation'))
      .getAllByRole('heading', { level: 2 })
      .map((item) => item.textContent)
  ).toEqual(['Daily work', 'Platform operations', 'Platform configuration'])
  expect(
    within(screen.getByRole('navigation'))
      .getAllByRole('heading', { level: 3 })
      .map((item) => item.textContent)
  ).toEqual([
    'Workbench',
    'My usage',
    'Chat sessions',
    'Users & assets',
    'Business & finance',
    'Tasks & diagnostics',
    'Models & channels',
    'Content & clients',
    'System management',
  ])
  const links = within(screen.getByRole('navigation'))
    .getAllByRole('link')
    .map((item) => item.getAttribute('href'))
  expect(new Set(links).size).toBe(links.length)
  expect(links).not.toEqual(
    expect.arrayContaining(['/profile', '/security', '/wallet'])
  )
})
it('ordinary users keep their logs and cannot see nested administration links or command entries', async () => {
  await setup(ROLE.USER)
  expect(
    screen.queryByRole('link', { name: 'Channels' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('link', { name: 'User Analytics' })
  ).not.toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'Task Logs' })).toBeVisible()
  fireEvent.keyDown(document, { key: 'k', ctrlKey: true })
  const dialog = await screen.findByRole('dialog')
  expect(
    within(dialog).getByRole('option', { name: /Workbench.*Overview/ })
  ).toBeVisible()
  expect(
    within(dialog).queryByRole('option', { name: /Business & finance/ })
  ).not.toBeInTheDocument()
})
it('administrators see only independently granted new sensitive links and search results', async () => {
  await setup(ROLE.ADMIN, undefined, { model_profits: { read: true } })
  expect(screen.getByRole('link', { name: 'Model profits' })).toBeVisible()
  expect(
    screen.queryByRole('link', { name: 'Balance ledger' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('link', { name: 'Customer service QR code' })
  ).not.toBeInTheDocument()
  fireEvent.keyDown(document, { key: 'k', ctrlKey: true })
  const dialog = await screen.findByRole('dialog')
  const input = within(dialog).getByRole('combobox')
  fireEvent.change(input, { target: { value: 'Model profits' } })
  expect(
    await within(dialog).findByRole('option', {
      name: /Business & finance.*Model profits/,
    })
  ).toBeVisible()
  expect(
    within(dialog).queryByRole('option', { name: /Balance ledger/ })
  ).not.toBeInTheDocument()
})
it('does not expose sensitive links while administrator self permissions are not loaded', async () => {
  await setup(ROLE.ADMIN)
  expect(
    screen.queryByRole('link', { name: 'Model profits' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('link', { name: 'Image and video operations' })
  ).not.toBeInTheDocument()
})
it('retains the administrator role floor even if an ordinary account receives a stale capability', async () => {
  await setup(ROLE.USER, undefined, {
    model_profits: { read: true },
    channel: { read: true },
    customer_service: { read: true },
  })
  expect(
    screen.queryByRole('link', { name: 'Model profits' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('link', { name: 'Channels' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('link', { name: 'Customer service QR code' })
  ).not.toBeInTheDocument()
})
it('platform disablement removes empty business parents and their command entries', async () => {
  await setup(ROLE.SUPER_ADMIN, { finance: { enabled: false } })
  expect(
    screen.queryByRole('heading', { name: 'Business & finance' })
  ).not.toBeInTheDocument()
  fireEvent.keyDown(document, { key: 'k', ctrlKey: true })
  const dialog = await screen.findByRole('dialog')
  expect(
    within(dialog).queryByRole('option', { name: /Model profits/ })
  ).not.toBeInTheDocument()
})
