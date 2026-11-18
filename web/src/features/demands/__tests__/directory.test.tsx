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
import { render, screen, cleanup, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, afterEach, test, expect, vi } from 'vitest'

import { DemandDirectory } from '../directory'
import { newDirectoryProfile } from '../directory-api'

const { get, post, put } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
}))
vi.mock('@/lib/api', () => ({ api: { get, post, put } }))
let client: QueryClient
const row = {
  ...newDirectoryProfile,
  id: 'profile-1',
  code: 'SVC-001',
  display_name: 'Website service',
  service_direction: 'Web development',
  revision: 1,
  enabled: true,
  published: true,
}
const response = (items: unknown[], more = false, cursor = '') => ({
  data: {
    success: true,
    data: { items, total: 31, next_after_id: cursor, has_more: more },
  },
})
function show(
  kind: 'service-profiles' | 'customer-profiles' = 'service-profiles',
  canWrite = true,
  onViewDemands = vi.fn()
) {
  return render(
    <QueryClientProvider client={client}>
      <DemandDirectory
        accountId={1}
        kind={kind}
        canWrite={canWrite}
        onViewDemands={onViewDemands}
      />
    </QueryClientProvider>
  )
}
beforeEach(() => {
  vi.resetAllMocks()
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  get.mockResolvedValue(response([row]))
  post.mockResolvedValue({
    data: {
      success: true,
      data: {
        created_service_profiles: 100,
        created_customer_profiles: 100,
        skipped_service_profiles: 0,
        skipped_customer_profiles: 0,
        replayed: false,
      },
    },
  })
})
afterEach(() => {
  cleanup()
  client.clear()
})
test('directory pages follow the server cursor and a new search restarts at the first page', async () => {
  get.mockImplementation(
    (_url: string, options: { params: { after_id: string } }) =>
      Promise.resolve(
        options.params.after_id
          ? response([
              {
                ...row,
                id: 'profile-2',
                code: 'SVC-031',
                display_name: 'Later service',
              },
            ])
          : response([row], true, 'opaque-next')
      )
  )
  const user = userEvent.setup()
  show()
  await screen.findByText('Website service')
  await user.click(screen.getByRole('button', { name: 'Next page' }))
  await screen.findByText('Later service')
  expect(get.mock.calls.at(-1)?.[1].params.after_id).toBe('opaque-next')
  await user.type(
    screen.getByRole('textbox', { name: 'Search profiles' }),
    'word'
  )
  await waitFor(() =>
    expect(get.mock.calls.at(-1)?.[1].params).toMatchObject({
      q: 'word',
      after_id: '',
    })
  )
  expect(screen.getByRole('button', { name: 'Previous page' })).toBeDisabled()
})
test('import requires explicit confirmation and sends one stable batch without creating accounts', async () => {
  const user = userEvent.setup()
  show()
  await screen.findByText('Website service')
  await user.click(
    screen.getByRole('button', {
      name: 'Import 100 service and 100 customer profiles',
    })
  )
  expect(post).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Continue' }))
  await screen.findByText('Import completed; existing records were retained.')
  expect(post).toHaveBeenCalledTimes(1)
  expect(post.mock.calls[0]?.[0]).toBe('/api/demand-management/profile-presets')
  expect(post.mock.calls[0]?.[1]).toEqual({
    batch_code: 'yunshu-platform-services-customers-100x100-v3',
  })
})
test('failed edit retains the typed profile and code stays immutable', async () => {
  get.mockImplementation((url: string) =>
    Promise.resolve(
      url.endsWith('/config')
        ? {
            data: {
              success: true,
              data: {
                revision: 1,
                config: {
                  enabled: true,
                  categories: [],
                  upload_rules: {
                    max_image_bytes: 1024,
                    max_video_bytes: 1024,
                    max_attachments: 1,
                    chunk_bytes: 65536,
                    allowed_mime_types: ['image/png'],
                    upload_ttl_seconds: 300,
                  },
                },
              },
            },
          }
        : response([row])
    )
  )
  put.mockRejectedValue(new Error('Conflict'))
  const user = userEvent.setup()
  show()
  await screen.findByText('Website service')
  await user.click(screen.getByRole('button', { name: 'Edit' }))
  expect(screen.getByRole('textbox', { name: 'Profile code' })).toBeDisabled()
  const name = screen.getByRole('textbox', { name: 'Display name' })
  await user.clear(name)
  await user.type(name, 'Changed name')
  await user.click(screen.getByRole('button', { name: 'Save profile' }))
  await screen.findByRole('alert')
  expect(name).toHaveValue('Changed name')
  expect(put.mock.calls[0]?.[1].revision).toBe(1)
})
test('read-only customer records hide mutations and linked accounts open the demand filter', async () => {
  get.mockResolvedValue(
    response([{ ...row, user_id: 9, organization: 'Studio' }])
  )
  const onView = vi.fn()
  const user = userEvent.setup()
  show('customer-profiles', false, onView)
  await screen.findByText('Website service')
  expect(screen.queryByRole('button', { name: 'Edit' })).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', {
      name: 'Import 100 service and 100 customer profiles',
    })
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'View demands' }))
  expect(onView).toHaveBeenCalledWith('profile-1')
})
