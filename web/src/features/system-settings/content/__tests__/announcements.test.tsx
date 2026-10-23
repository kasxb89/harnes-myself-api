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
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import {
  announcementSchema,
  announcementTitle,
  parseAnnouncements,
  serializeAnnouncements,
} from '../announcements'
import { AnnouncementsSection } from '../announcements-section'

const legacy = {
  id: 1,
  content: '**Existing** announcement',
  publishDate: '2026-10-22T08:00:00Z',
  type: 'default',
}

function renderSettings(data: string = JSON.stringify([legacy])) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const view = render(
    <QueryClientProvider client={client}>
      <AnnouncementsSection enabled data={data} />
    </QueryClientProvider>
  )
  return {
    client,
    ...view,
    setData: (value: string) =>
      view.rerender(
        <QueryClientProvider client={client}>
          <AnnouncementsSection enabled data={value} />
        </QueryClientProvider>
      ),
  }
}

beforeEach(() => {
  vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
})

test('legacy rows derive a readable title and retain unknown update times', () => {
  const [item] = parseAnnouncements(JSON.stringify([legacy]))
  expect(announcementTitle(item)).toBe('Existing announcement')
  expect(item.updatedAt).toBeNull()
  renderSettings()
  expect(
    screen.getByRole('checkbox', {
      name: 'Select announcement Existing announcement',
    })
  ).toBeVisible()
  expect(screen.getByText('Unknown')).toBeVisible()
})

test('missing and duplicate legacy ids receive distinct identities without colliding with explicit ids', () => {
  const values = [
    { ...legacy, id: undefined },
    legacy,
    { ...legacy, id: 1 },
    { ...legacy, id: 3 },
  ]
  expect(
    parseAnnouncements(JSON.stringify(values)).map((item) => item.id)
  ).toEqual([2, 1, 4, 3])
})

test('serialization sends editable fields while omitting server timestamps and unknown stored fields', () => {
  const [item] = parseAnnouncements(
    JSON.stringify([
      {
        ...legacy,
        title: 'Service update',
        updatedAt: '2026-10-23T00:00:00Z',
        secret: 'never expose',
      },
    ])
  )
  expect(JSON.parse(serializeAnnouncements([item]))).toEqual([
    { ...legacy, title: 'Service update' },
  ])
})

test.each([
  { title: 't'.repeat(121) },
  { content: ' ' },
  { content: 'c'.repeat(501) },
  { extra: 'e'.repeat(101) },
  { publishDate: 'invalid' },
  { publishDate: '2026-02-30T00:00:00Z' },
  { publishDate: '2026-10-22T08:00:00' },
])('invalid form input %j cannot be saved', (overrides) => {
  expect(
    announcementSchema.safeParse({ ...legacy, ...overrides }).success
  ).toBe(false)
})

test('editing a title preserves content and displays server time as read-only metadata', async () => {
  const user = userEvent.setup()
  renderSettings(
    JSON.stringify([{ ...legacy, updatedAt: '2026-10-23T00:00:00Z' }])
  )
  await user.click(screen.getByRole('button', { name: 'Edit' }))
  const dialog = await screen.findByRole('dialog', {
    name: 'Edit Announcement',
  })
  expect(within(dialog).getByRole('textbox', { name: 'Content' })).toHaveValue(
    legacy.content
  )
  expect(
    within(dialog).queryByRole('textbox', { name: 'Server update time' })
  ).not.toBeInTheDocument()
  expect(
    within(dialog).getByText(
      'Set by the server after saving. Older announcements may have no recorded update time.'
    )
  ).toBeVisible()
  fireEvent.change(
    within(dialog).getByRole('textbox', {
      name: 'Announcement title (optional)',
    }),
    { target: { value: 'New title' } }
  )
  await user.click(within(dialog).getByRole('button', { name: 'Update' }))
  await user.click(screen.getByRole('button', { name: 'Save Settings' }))
  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  const request = vi.mocked(api.put).mock.calls[0][1] as { value: string }
  expect(JSON.parse(request.value)).toEqual([
    { ...legacy, title: 'New title', extra: '' },
  ])
  expect(screen.getByText('New title')).toBeVisible()
})

test('an empty announcement body shows an accessible validation error and keeps the editor open', async () => {
  const user = userEvent.setup()
  renderSettings('[]')
  await user.click(screen.getByRole('button', { name: 'Add Announcement' }))
  const dialog = await screen.findByRole('dialog', { name: 'Add Announcement' })
  await user.click(within(dialog).getByRole('button', { name: 'Add' }))
  expect(await within(dialog).findByText('Content is required')).toBeVisible()
  expect(
    within(dialog).getByRole('textbox', { name: 'Content' })
  ).toHaveAttribute('aria-invalid', 'true')
  expect(api.put).not.toHaveBeenCalled()
})

test('a failed save keeps the edited draft through a server refetch and does not resend automatically', async () => {
  const user = userEvent.setup()
  vi.mocked(api.put).mockResolvedValue({
    data: { success: false, message: 'Save failed' },
  })
  const view = renderSettings()
  await user.click(screen.getByRole('button', { name: 'Edit' }))
  const dialog = await screen.findByRole('dialog', {
    name: 'Edit Announcement',
  })
  fireEvent.change(
    within(dialog).getByRole('textbox', {
      name: 'Announcement title (optional)',
    }),
    { target: { value: 'Unsaved title' } }
  )
  await user.click(within(dialog).getByRole('button', { name: 'Update' }))
  await user.click(screen.getByRole('button', { name: 'Save Settings' }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save Settings' })).toBeEnabled()
  )
  view.setData(JSON.stringify([{ ...legacy, title: 'Server title' }]))
  expect(screen.getByText('Unsaved title')).toBeVisible()
  expect(screen.queryByText('Server title')).not.toBeInTheDocument()
  expect(api.put).toHaveBeenCalledTimes(1)
})

test('toggling the announcement setting refreshes status and preserves a dirty list', async () => {
  const user = userEvent.setup()
  const view = renderSettings()
  const invalidate = vi.spyOn(view.client, 'invalidateQueries')
  await user.click(screen.getByRole('button', { name: 'Edit' }))
  const dialog = await screen.findByRole('dialog', {
    name: 'Edit Announcement',
  })
  fireEvent.change(
    within(dialog).getByRole('textbox', {
      name: 'Announcement title (optional)',
    }),
    { target: { value: 'Local title' } }
  )
  await user.click(within(dialog).getByRole('button', { name: 'Update' }))
  await user.click(screen.getByRole('switch', { name: 'Enabled' }))
  await waitFor(() =>
    expect(invalidate).toHaveBeenCalledWith({ queryKey: ['status'] })
  )
  view.setData(JSON.stringify([{ ...legacy, updatedAt: null }]))
  expect(screen.getByText('Local title')).toBeVisible()
  expect(screen.getByRole('button', { name: 'Save Settings' })).toBeEnabled()
  expect(api.put).toHaveBeenCalledWith('/api/option/', {
    key: 'console_setting.announcements_enabled',
    value: false,
  })
  await user.click(screen.getByRole('button', { name: 'Save Settings' }))
  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(2))
  expect(
    invalidate.mock.calls.filter(
      ([filters]) => filters?.queryKey?.[0] === 'status'
    )
  ).toHaveLength(2)
})

test('malformed stored data cannot be overwritten through an empty draft', () => {
  renderSettings('[null]')
  expect(screen.getByRole('alert')).toHaveTextContent(
    'Unable to read announcements.'
  )
  expect(
    screen.getByRole('button', { name: 'Add Announcement' })
  ).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Save Settings' })).toBeDisabled()
})

test('a full list disables adding while preserving selection and edit access', () => {
  renderSettings(
    JSON.stringify(
      Array.from({ length: 100 }, (_, index) => ({ ...legacy, id: index + 1 }))
    )
  )
  expect(
    screen.getByRole('button', { name: 'Add Announcement' })
  ).toBeDisabled()
  expect(
    screen.getByRole('checkbox', { name: 'Select all announcements' })
  ).toBeEnabled()
  expect(screen.getAllByRole('button', { name: 'Edit' })[0]).toBeEnabled()
})

test('over-limit legacy arrays never trust raw update timestamps and remain repairable by deletion', () => {
  const data = JSON.stringify(
    Array.from({ length: 101 }, (_, index) => ({
      ...legacy,
      id: index + 1,
      updatedAt: '2099-01-01T00:00:00Z',
    }))
  )
  expect(
    parseAnnouncements(data).every((item) => item.updatedAt === null)
  ).toBe(true)
  renderSettings(data)
  expect(screen.getByRole('alert')).toHaveTextContent(
    'Maximum 100 announcements'
  )
  expect(screen.getAllByRole('button', { name: 'Edit' })[0]).toBeEnabled()
})
