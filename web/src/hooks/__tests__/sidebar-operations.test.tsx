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
            {group.items.map((item) =>
              item.url ? (
                <a key={item.url} href={item.url}>
                  {item.title}
                </a>
              ) : null
            )}
          </section>
        ))}
      </nav>
    </SearchProvider>
  )
}
async function setup(role: number = ROLE.ADMIN, adminConfig?: object) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  })
  clients.push(client)
  client.setQueryData(['status'], {
    SidebarModulesAdmin: adminConfig ? JSON.stringify(adminConfig) : '',
  })
  useAuthStore.getState().auth.setUser({ id: 1, username: 'test', role })
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

it('places the dedicated operational entries immediately below General and before Personal for administrators', async () => {
  await setup()
  const groups = within(screen.getByRole('navigation'))
    .getAllByRole('heading', { level: 2 })
    .map((heading) => heading.textContent)
  expect(groups.indexOf('Operations management')).toBe(
    groups.indexOf('General') + 1
  )
  expect(groups.indexOf('Personal')).toBe(
    groups.indexOf('Operations management') + 1
  )
  const operations = screen.getByRole('region', {
    name: 'Operations management',
  })
  expect(
    within(operations)
      .getAllByRole('link')
      .map((link) => link.textContent)
  ).toEqual([
    'Operations dashboard',
    'Model profits',
    'Image and video operations',
    'Balance ledger',
    'User Activity',
    'API Error Logs',
    'Client model configuration',
    'AI Creation Management',
  ])
  expect(
    within(screen.getByRole('region', { name: 'Admin' })).queryByRole('link', {
      name: 'User Activity',
    })
  ).not.toBeInTheDocument()
})
it('hides the whole operations group from ordinary users including its command search entries', async () => {
  await setup(ROLE.USER)
  expect(
    screen.queryByRole('region', { name: 'Operations management' })
  ).not.toBeInTheDocument()
  fireEvent.keyDown(document, { key: 'k', ctrlKey: true })
  const dialog = await screen.findByRole('dialog')
  expect(within(dialog).getByRole('option', { name: 'Overview' })).toBeVisible()
  expect(
    within(dialog).queryByRole('option', { name: 'Balance ledger' })
  ).not.toBeInTheDocument()
  expect(
    within(dialog).queryByRole('option', { name: 'Model profits' })
  ).not.toBeInTheDocument()
  expect(
    within(dialog).queryByRole('option', { name: 'User Activity' })
  ).not.toBeInTheDocument()
  expect(
    within(dialog).queryByRole('option', { name: 'Image and video operations' })
  ).not.toBeInTheDocument()
})
it('preserves existing module visibility settings in the new group and command search', async () => {
  await setup(ROLE.ADMIN, { admin: { enabled: true, setting: false } })
  expect(
    screen.queryByRole('link', { name: 'Balance ledger' })
  ).not.toBeInTheDocument()
  expect(screen.getByRole('link', { name: 'User Activity' })).toBeVisible()
  fireEvent.keyDown(document, { key: 'k', ctrlKey: true })
  const dialog = await screen.findByRole('dialog')
  expect(
    within(dialog).queryByRole('option', { name: 'Balance ledger' })
  ).not.toBeInTheDocument()
  expect(
    within(dialog).getByRole('option', { name: 'User Activity' })
  ).toBeVisible()
})
it('does not leave an empty operations heading when all contributing modules are disabled', async () => {
  await setup(ROLE.ADMIN, {
    admin: { enabled: false },
    console: { enabled: true, log: false },
  })
  expect(
    screen.queryByRole('region', { name: 'Operations management' })
  ).not.toBeInTheDocument()
  expect(screen.getByRole('region', { name: 'General' })).toBeVisible()
})
