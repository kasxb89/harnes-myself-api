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
import { act, cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it } from 'vitest'

import { SidebarProvider } from '@/components/ui/sidebar'

import { NavGroup } from '../components/nav-group'
import type { NavGroup as NavGroupData } from '../types'

const clients: QueryClient[] = []
afterEach(() => {
  cleanup()
  clients.splice(0).forEach((client) => client.clear())
})
async function setup(
  group: NavGroupData,
  path = '/',
  collapsed = false,
  chats: unknown[] = []
) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, staleTime: Infinity } },
  })
  clients.push(client)
  client.setQueryData(['status'], { Chats: JSON.stringify(chats) })
  const root = createRootRoute({
    component: () => (
      <SidebarProvider defaultOpen={!collapsed}>
        <NavGroup {...group} />
      </SidebarProvider>
    ),
  })
  const router = createRouter({
    routeTree: root,
    history: createMemoryHistory({ initialEntries: [path] }),
  })
  await router.load()
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  return router
}
const tasks: NavGroupData = {
  title: 'Platform operations',
  items: [
    {
      title: 'Tasks & diagnostics',
      items: [
        {
          title: 'Task Logs',
          url: '/usage-logs/task',
          activeUrls: ['/usage-logs/drawing'],
        },
        { title: 'API Error Logs', url: '/usage-logs/errors' },
      ],
    },
  ],
}
it('opens the consolidated parent and marks the task link active for the old drawing URL', async () => {
  await setup(tasks, '/usage-logs/drawing')
  expect(
    screen.getByRole('button', { name: 'Tasks & diagnostics' })
  ).toHaveAttribute('aria-expanded', 'true')
  const link = screen.getByRole('link', { name: 'Task Logs' })
  expect(link).toBeVisible()
  expect(link).toHaveAttribute('data-active')
})
it('supports keyboard expansion and collapse without navigating the parent', async () => {
  const user = userEvent.setup()
  await setup(tasks)
  const parent = screen.getByRole('button', { name: 'Tasks & diagnostics' })
  expect(parent).toHaveAttribute('aria-expanded', 'false')
  parent.focus()
  await user.keyboard('{Enter}')
  expect(parent).toHaveAttribute('aria-expanded', 'true')
  expect(screen.getByRole('link', { name: 'API Error Logs' })).toBeVisible()
  await user.keyboard('{Enter}')
  expect(parent).toHaveAttribute('aria-expanded', 'false')
})
it('shows child destinations in the collapsed sidebar dropdown', async () => {
  const user = userEvent.setup()
  await setup(tasks, '/', true)
  const parent = screen.getByRole('button', { name: 'Tasks & diagnostics' })
  await user.click(parent)
  const menu = await screen.findByRole('menu')
  expect(
    within(menu).getByRole('menuitem', { name: 'Task Logs' })
  ).toHaveAttribute('href', '/usage-logs/task')
  expect(
    within(menu).getByRole('menuitem', { name: 'API Error Logs' })
  ).toHaveAttribute('href', '/usage-logs/errors')
  await user.keyboard('{Escape}')
  expect(screen.queryByRole('menu')).not.toBeInTheDocument()
})
it('keeps the playground available inside chat sessions when no presets are configured', async () => {
  const user = userEvent.setup()
  await setup({
    title: 'Daily work',
    items: [
      {
        title: 'Chat sessions',
        type: 'chat-presets',
        links: [{ title: 'Playground', url: '/playground' }],
      },
    ],
  })
  await user.click(screen.getByRole('button', { name: 'Chat sessions' }))
  expect(screen.getByRole('link', { name: 'Playground' })).toHaveAttribute(
    'href',
    '/playground'
  )
})
it('honors disabled chat presets while retaining the permitted static playground', async () => {
  const user = userEvent.setup()
  await setup(
    {
      title: 'Daily work',
      items: [
        {
          title: 'Chat sessions',
          type: 'chat-presets',
          showPresets: false,
          links: [{ title: 'Playground', url: '/playground' }],
        },
      ],
    },
    '/',
    true,
    [{ name: 'Configured chat', url: 'https://chat.example.invalid' }]
  )
  await user.click(screen.getByRole('button', { name: 'Chat sessions' }))
  const menu = await screen.findByRole('menu')
  expect(
    within(menu).getByRole('menuitem', { name: 'Playground' })
  ).toBeVisible()
  expect(within(menu).queryByText('Configured chat')).not.toBeInTheDocument()
})

it('automatically opens chat sessions after command navigation to the playground', async () => {
  const router = await setup({
    title: 'Daily work',
    items: [
      {
        title: 'Chat sessions',
        type: 'chat-presets',
        links: [{ title: 'Playground', url: '/playground' }],
      },
    ],
  })
  expect(screen.getByRole('button', { name: 'Chat sessions' })).toHaveAttribute(
    'aria-expanded',
    'false'
  )
  await act(async () => {
    await router.navigate({ to: '/playground' })
  })
  expect(screen.getByRole('button', { name: 'Chat sessions' })).toHaveAttribute(
    'aria-expanded',
    'true'
  )
  expect(screen.getByRole('link', { name: 'Playground' })).toBeVisible()
})
