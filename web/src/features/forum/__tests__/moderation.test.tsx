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
} from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { useAuthStore } from '@/stores/auth-store'

import { ForumManagementPage } from '..'

const { get, put, remove } = vi.hoisted(() => ({
  get: vi.fn(),
  put: vi.fn(),
  remove: vi.fn(),
}))
vi.mock('@/lib/api', () => ({ api: { get, put, delete: remove } }))
const clients: QueryClient[] = []
const row = {
  id: 'post-fixture',
  title: 'A community discussion',
  body: 'Published before review',
  author: { id: 1, display_name: 'Synthetic member' },
  created_at: 1700000000,
}
const envelope = (data: unknown) => ({ data: { success: true, data } })
function view() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  clients.push(client)
  return render(
    <QueryClientProvider client={client}>
      <ForumManagementPage />
    </QueryClientProvider>
  )
}
beforeEach(() => {
  useAuthStore.getState().auth.setUser({ id: 9, username: 'admin', role: 100 })
  get.mockImplementation((url: string) =>
    Promise.resolve(
      envelope(
        url.endsWith('/config')
          ? { revision: 1, enabled: true }
          : { items: [row], next_cursor: '', has_more: false }
      )
    )
  )
  put.mockResolvedValue(envelope({}))
  remove.mockResolvedValue(envelope({}))
})
afterEach(() => {
  cleanup()
  clients.splice(0).forEach((c) => c.clear())
  vi.resetAllMocks()
  useAuthStore.getState().auth.reset()
})
test('marks an already published post reviewed without an approval gate', async () => {
  view()
  fireEvent.click(await screen.findByText(row.title))
  fireEvent.click(screen.getByRole('button', { name: 'Mark reviewed' }))
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith(
      '/api/forum-management/posts/post-fixture/review',
      { reviewed: true }
    )
  )
  expect(remove).not.toHaveBeenCalled()
})
test('removes content without author approval and retains a moderation reason', async () => {
  view()
  fireEvent.click(await screen.findByText(row.title))
  fireEvent.click(screen.getByRole('button', { name: 'Remove content' }))
  fireEvent.change(
    screen.getByRole('textbox', { name: 'Removal reason (optional)' }),
    { target: { value: 'Spam' } }
  )
  fireEvent.click(screen.getByRole('button', { name: 'Remove' }))
  await waitFor(() =>
    expect(remove).toHaveBeenCalledWith(
      '/api/forum-management/posts/post-fixture',
      { data: { reason: 'Spam' } }
    )
  )
})
test('read-only moderators see content without removal or review actions', async () => {
  useAuthStore.getState().auth.setUser({
    id: 9,
    username: 'limited',
    role: 10,
    permissions: {
      admin_permissions: { forum: { read: true, write: false } },
    },
  })
  view()
  fireEvent.click(await screen.findByText(row.title))
  expect(screen.queryByRole('button', { name: 'Remove content' })).toBeNull()
  expect(screen.queryByRole('button', { name: 'Mark reviewed' })).toBeNull()
})
