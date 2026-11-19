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

import { DemandManagementPage } from '..'
import { configSchema } from '../api'
import type { CommunityOrder } from '../community-api'
import { CommunityConversation } from '../community-conversation'

const { get, post, put } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
}))
vi.mock('@/lib/api', () => ({ api: { get, post, put } }))
const order: CommunityOrder = {
  id: 'community-fixture',
  number: 'YSC-TEST',
  title: 'A real user project',
  status: 'open',
  revision: 1,
  listing_visible: true,
  publisher: { id: 1, display_name: 'Publisher A', phone_e164: '+15550001000' },
  acceptor: null,
  my_role: 'admin',
  listing: {
    public_summary: 'Public overview only',
    public_budget: '',
    service_profile_name: 'Website',
    public_attachments: [],
  },
  private_text: 'Only participants and administrators',
  last_seq: 1,
  last_read_seq: 0,
  unread_count: 1,
  created_at: 1700000000,
  updated_at: 1700000000,
  can_send: true,
  can_cancel: true,
  can_reopen: false,
}
const envelope = (data: unknown) => ({ data: { success: true, data } })
const rules = configSchema.shape.config.shape.upload_rules.parse({
  max_image_bytes: 20 * 1024 * 1024,
  max_video_bytes: 512 * 1024 * 1024,
  max_attachments: 8,
  chunk_bytes: 1048576,
  allowed_mime_types: ['image/png', 'video/mp4'],
  upload_ttl_seconds: 86400,
})
const clients: QueryClient[] = []
function view(element: React.ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  clients.push(client)
  return render(
    <QueryClientProvider client={client}>{element}</QueryClientProvider>
  )
}
beforeEach(() => {
  sessionStorage.clear()
  useAuthStore.getState().auth.setUser({
    id: 9,
    username: 'limited-community',
    role: 10,
    permissions: {
      admin_permissions: {
        demands: { read: false, write: false },
        community_demands: { read: true, write: true },
      },
    },
  })
  get.mockImplementation((url: string) => {
    if (url.endsWith('/config')) {
      return Promise.resolve(
        envelope({ revision: 1, enabled: true, upload_rules: rules })
      )
    }
    if (url.endsWith('/messages')) {
      return Promise.resolve(
        envelope({
          items: [],
          order,
          last_seq: 1,
          next_after_seq: 1,
          has_more: false,
        })
      )
    }
    if (url.endsWith('/audit')) {
      return Promise.resolve(envelope({ items: [], has_more: false }))
    }
    return Promise.resolve(
      envelope({ items: [order], has_more: false, next_cursor: '' })
    )
  })
})
afterEach(() => {
  cleanup()
  clients.splice(0).forEach((c) => c.clear())
  vi.resetAllMocks()
  sessionStorage.clear()
  useAuthStore.getState().auth.reset()
})
test('a community-only administrator opens peer orders without requesting private official demands', async () => {
  view(<DemandManagementPage />)
  expect(
    await screen.findByRole('tab', { name: 'Community orders' })
  ).toBeVisible()
  expect(screen.queryByRole('tab', { name: 'Demands' })).toBeNull()
  fireEvent.click(
    await screen.findByRole('button', { name: /A real user project/ })
  )
  expect(
    await screen.findByRole('textbox', { name: 'Reply to both participants' })
  ).toBeVisible()
  expect(
    get.mock.calls.every(
      ([url]) => !String(url).startsWith('/api/demand-management')
    )
  ).toBe(true)
})
test('a lost administrator reply retains its original key and retries the same packet', async () => {
  post
    .mockRejectedValueOnce(new Error('connection lost'))
    .mockImplementation(
      (_url: string, body: { client_message_id: string; text: string }) =>
        Promise.resolve(
          envelope({
            order,
            message: {
              id: 'message-one',
              order_id: order.id,
              seq: 2,
              sender_id: 9,
              author_kind: 'admin',
              author_name: 'Administrator A',
              kind: 'message',
              text: body.text,
              attachments: [],
              created_at: 1700000001,
              client_message_id: body.client_message_id,
              read: false,
            },
            replayed: true,
          })
        )
    )
  view(
    <CommunityConversation
      initial={order}
      accountId={9}
      canWrite
      onBack={() => {}}
    />
  )
  const input = await screen.findByRole('textbox', {
    name: 'Reply to both participants',
  })
  fireEvent.change(input, { target: { value: 'Administrator direct reply' } })
  fireEvent.click(screen.getByRole('button', { name: 'Send' }))
  await screen.findByRole('button', { name: 'Retry same message' })
  await waitFor(() => expect(post).toHaveBeenCalledTimes(1))
  const first = post.mock.calls[0][1]
  expect(
    sessionStorage.getItem('community-admin-message:9:community-fixture')
  ).toContain(first.client_message_id)
  fireEvent.click(screen.getByRole('button', { name: 'Retry same message' }))
  await waitFor(() => expect(post).toHaveBeenCalledTimes(2))
  expect(post.mock.calls[1][1]).toEqual(first)
  expect(await screen.findByText('Administrator direct reply')).toBeVisible()
  expect(
    sessionStorage.getItem('community-admin-message:9:community-fixture')
  ).toBeNull()
})
test('read-only moderators can inspect participants and history without replying or cancelling', async () => {
  view(
    <CommunityConversation
      initial={order}
      accountId={9}
      canWrite={false}
      onBack={() => {}}
    />
  )
  expect(
    screen.queryByRole('textbox', { name: 'Reply to both participants' })
  ).toBeNull()
  fireEvent.click(screen.getByRole('button', { name: 'Order details' }))
  expect(await screen.findByText('+15550001000')).toBeVisible()
  expect(screen.queryByRole('button', { name: 'Cancel order' })).toBeNull()
  expect(
    screen.queryByRole('button', { name: 'Hide public listing' })
  ).toBeNull()
})
