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
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { useAuthStore } from '@/stores/auth-store'

import { DemandManagementPage } from '..'
import {
  configSchema,
  uploadDemandFile,
  type Demand,
  type DemandConfig,
  type Engineer,
  type Message,
} from '../api'
import { DemandSettings } from '../config-editor'
import { DemandConversation } from '../conversation'

const { get, post, put } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
}))
vi.mock('@/lib/api', () => ({ api: { get, post, put } }))
const clients: QueryClient[] = []
const engineer: Engineer = {
  id: 'engineer-fixture',
  user_id: 2,
  display_name: 'SYNTHETIC engineer',
  avatar_attachment_id: '',
  category_ids: ['software'],
  introduction: '',
  service_hours: '',
  published: true,
  enabled: true,
  accepting: true,
  capacity: 10,
  sort: 0,
  revision: 1,
  presence: 'offline',
  can_start: true,
  open_demand_count: 1,
}
const demand: Demand = {
  id: 'demand-fixture',
  number: 'YS-FIXTURE',
  title: 'SYNTHETIC website',
  user_id: 1,
  engineer_id: engineer.id,
  status: 'communicating',
  priority: 'normal',
  revision: 2,
  last_seq: 1,
  last_read_seq: 0,
  unread_count: 1,
  preview: 'Hello',
  budget: '',
  expected_date: '',
  created_at: 1700000000,
  updated_at: 1700000000,
  delivery_id: '',
  engineer,
}
const message: Message = {
  id: 'message-fixture',
  demand_id: demand.id,
  seq: 1,
  sender_id: 1,
  author: 'user',
  kind: 'message',
  text: 'Customer message',
  created_at: 1700000000,
  client_message_id: 'client-fixture-key',
  read: false,
  attachments: [],
}
const config: DemandConfig = {
  revision: 1,
  config: {
    enabled: true,
    categories: [{ id: 'software', name: 'Software', enabled: true }],
    upload_rules: {
      max_image_bytes: 20971520,
      max_video_bytes: 536870912,
      max_attachments: 8,
      chunk_bytes: 4194304,
      upload_ttl_seconds: 86400,
      allowed_mime_types: ['image/png', 'video/mp4'],
    },
  },
}
const envelope = (data: unknown) => ({ data: { success: true, data } })
function view(node: React.ReactNode) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  clients.push(client)
  return render(
    <QueryClientProvider client={client}>{node}</QueryClientProvider>
  )
}
beforeEach(() => {
  sessionStorage.clear()
  get.mockImplementation((url: string) => {
    if (url.endsWith('/audit')) {
      return Promise.resolve(
        envelope({ items: [], next_after_id: 0, has_more: false })
      )
    }
    if (url.endsWith('/config')) return Promise.resolve(envelope(config))
    return Promise.resolve(
      envelope({
        items: [message],
        next_after_seq: 1,
        last_seq: 1,
        has_more: false,
        demand,
      })
    )
  })
  post.mockResolvedValue(envelope({ last_read_seq: 1, unread_count: 0 }))
})
afterEach(() => {
  vi.unstubAllGlobals()
  cleanup()
  for (const client of clients) client.clear()
  clients.length = 0
  vi.resetAllMocks()
  sessionStorage.clear()
  useAuthStore.getState().auth.reset()
})

test('an administrator without demand read permission cannot load customer demands', () => {
  useAuthStore.getState().auth.setUser({
    id: 9,
    username: 'limited',
    role: 10,
    permissions: {
      admin_permissions: { demands: { read: false, write: false } },
    },
  })
  view(<DemandManagementPage />)
  expect(screen.getByText('Access denied')).toBeInTheDocument()
  expect(get).not.toHaveBeenCalled()
})

test('an unknown send result retains one exact outbox body across retry and remount', async () => {
  const user = userEvent.setup()
  const submitted: unknown[] = []
  post.mockImplementation((url: string, body: unknown) => {
    if (!url.endsWith('/messages')) {
      return Promise.resolve(envelope({ last_read_seq: 1, unread_count: 0 }))
    }
    submitted.push(body)
    return submitted.length === 1
      ? Promise.reject(new Error('lost response'))
      : Promise.resolve(
          envelope({
            demand,
            message: {
              ...message,
              id: 'engineer-reply',
              sender_id: 2,
              seq: 2,
              author: 'engineer',
              text: 'I can help',
            },
            replayed: true,
          })
        )
  })
  const page = view(
    <DemandConversation
      initial={demand}
      accountId={2}
      mode='engineer'
      canWrite
      engineers={[]}
      onBack={() => undefined}
    />
  )
  await screen.findByText('Customer message')
  await user.type(screen.getByLabelText('Message to customer'), 'I can help')
  await user.click(screen.getByRole('button', { name: /^Send$/ }))
  await screen.findByText(
    'Message delivery is unconfirmed. Your draft and submission key are retained.'
  )
  expect(screen.getByLabelText('Message to customer')).toBeDisabled()
  const stored = sessionStorage.getItem(`demand-message:2:${demand.id}`)
  expect(stored).toContain('I can help')
  page.unmount()
  view(
    <DemandConversation
      initial={demand}
      accountId={2}
      mode='engineer'
      canWrite
      engineers={[]}
      onBack={() => undefined}
    />
  )
  await user.click(screen.getByRole('button', { name: 'Retry same message' }))
  await waitFor(() => expect(submitted).toHaveLength(2))
  expect(submitted[1]).toEqual(submitted[0])
  await waitFor(() =>
    expect(sessionStorage.getItem(`demand-message:2:${demand.id}`)).toBeNull()
  )
})

test('internal notes use the administrator action endpoint and never the public message endpoint', async () => {
  post.mockResolvedValue(envelope({ demand, replayed: false }))
  const user = userEvent.setup()
  view(
    <DemandConversation
      initial={demand}
      accountId={9}
      mode='admin'
      canWrite
      engineers={[engineer]}
      onBack={() => undefined}
    />
  )
  await screen.findByText('Customer message')
  await user.type(screen.getByLabelText('Internal note'), 'PRIVATE note')
  await user.click(screen.getByRole('button', { name: 'Add internal note' }))
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      `/api/demand-management/${demand.id}/actions`,
      expect.objectContaining({
        action: 'internal_note',
        text: 'PRIVATE note',
        revision: 2,
      })
    )
  )
  expect(
    post.mock.calls.some(([path]) => String(path).endsWith('/messages'))
  ).toBe(false)
})

test('a rejected settings save keeps typed values and the original revision', async () => {
  put.mockRejectedValue(new Error('revision conflict'))
  const user = userEvent.setup()
  view(<DemandSettings accountId={9} canWrite />)
  const input = await screen.findByLabelText('Category name')
  fireEvent.change(input, { target: { value: 'My software category' } })
  await user.click(screen.getByRole('button', { name: 'Save settings' }))
  await screen.findByText(
    'Settings were not saved. Reload the latest revision before retrying; your edits are retained.'
  )
  expect(input).toHaveValue('My software category')
  expect(put).toHaveBeenCalledWith(
    '/api/demand-management/config',
    expect.objectContaining({ revision: 1 })
  )
})

test('the upload resumes at the acknowledged offset without submitting the first chunk again', async () => {
  const file = new File(['0123456789'], 'sample.png', { type: 'image/png' })
  const attachment = {
    id: 'upload-fixture',
    name: file.name,
    kind: 'image',
    mime_type: file.type,
    size_bytes: 10,
    status: 'uploading',
    uploaded_bytes: 4,
  }
  let offset = 4
  post.mockImplementation((path: string) =>
    Promise.resolve(
      envelope({
        attachment: {
          ...attachment,
          status: path.endsWith('/complete') ? 'ready' : 'uploading',
        },
        offset,
        chunk_bytes: 4,
        expires_at: 1700000500,
      })
    )
  )
  const offsets: string[] = []
  put.mockImplementation(
    (
      _path: string,
      body: Blob,
      options: { headers: Record<string, string> }
    ) => {
      offsets.push(options.headers['Upload-Offset'])
      offset += body.size
      return Promise.resolve(
        envelope({ attachment, offset, chunk_bytes: 4, expires_at: 1700000500 })
      )
    }
  )
  const result = await uploadDemandFile(
    file,
    'stable-upload-key',
    () => undefined
  )
  expect(offsets).toEqual(['4', '8'])
  expect(result.status).toBe('ready')
  expect(post.mock.calls[0]?.[1]).toMatchObject({
    client_upload_id: 'stable-upload-key',
  })
})

test('typed configuration rejects excessive video size and malformed categories', () => {
  const oversized = structuredClone(config)
  oversized.config.upload_rules.max_video_bytes = 3 * 1024 ** 3
  expect(configSchema.safeParse(oversized).success).toBe(false)
  const invalid = structuredClone(config)
  invalid.config.categories[0].id = '../../private'
  expect(configSchema.safeParse(invalid).success).toBe(false)
})

test('a failed local receipt write keeps the draft and does not send a message', async () => {
  const user = userEvent.setup()
  view(
    <DemandConversation
      initial={demand}
      accountId={2}
      mode='engineer'
      canWrite
      engineers={[]}
      onBack={() => undefined}
    />
  )
  await screen.findByText('Customer message')
  await user.type(screen.getByLabelText('Message to customer'), 'Keep my draft')
  const originalStorage = sessionStorage
  vi.stubGlobal('sessionStorage', {
    getItem: originalStorage.getItem.bind(originalStorage),
    setItem: () => {
      throw new Error('storage unavailable')
    },
    removeItem: originalStorage.removeItem.bind(originalStorage),
    clear: originalStorage.clear.bind(originalStorage),
  })
  await user.click(screen.getByRole('button', { name: /^Send$/ }))
  await screen.findByText(
    'Unable to save the submission receipt locally. Nothing was sent.'
  )
  expect(screen.getByLabelText('Message to customer')).toHaveValue(
    'Keep my draft'
  )
  expect(
    post.mock.calls.some(([url]) => String(url).endsWith('/messages'))
  ).toBe(false)
  vi.unstubAllGlobals()
})

test('the peer cursor updates the read receipt for a sent message', async () => {
  get.mockImplementation((url: string) =>
    Promise.resolve(
      envelope(
        url.endsWith('/config')
          ? config
          : {
              items: [{ ...message, sender_id: 2 }],
              next_after_seq: 1,
              last_seq: 1,
              peer_read_seq: 1,
              has_more: false,
              demand,
            }
      )
    )
  )
  view(
    <DemandConversation
      initial={demand}
      accountId={2}
      mode='engineer'
      canWrite
      engineers={[]}
      onBack={() => undefined}
    />
  )
  await screen.findByText('Read')
})
