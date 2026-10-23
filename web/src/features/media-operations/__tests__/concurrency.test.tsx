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
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { AxiosError, AxiosHeaders } from 'axios'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import fr from '@/i18n/locales/fr.json'
import ja from '@/i18n/locales/ja.json'
import ru from '@/i18n/locales/ru.json'
import viLocale from '@/i18n/locales/vi.json'
import zhTW from '@/i18n/locales/zh-TW.json'
import zh from '@/i18n/locales/zh.json'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { MediaOperationsPage } from '..'
import { MediaConcurrencyPanel } from '../components/concurrency-panel'
import type { MediaConcurrency } from '../concurrency-schema'

const i18n = createInstance()
const clients: QueryClient[] = []
const snapshot: MediaConcurrency = {
  schema_version: 1,
  revision: 'config-one',
  defaults: { video: 4, image: 10 },
  pool_overrides: [{ pool_id: 'opaque-historical', limit: 3 }],
  pools: [
    ...[1, 2, 3, 4].map((id) => ({
      pool_id: `opaque-h3-${id}`,
      label: `H3 account ${id}`,
      media_kind: 'video' as const,
      channel_ids: [15 + id],
      configured_limit: 4,
      known_upstream_limit: 4,
      effective_limit: 4,
      occupied: id === 1 ? 5 : 0,
      unknown: id === 1 ? 1 : 0,
      enabled: true,
      reconciled: true,
      available_slots: id === 1 ? 0 : 4,
    })),
    {
      pool_id: 'opaque-image',
      label: 'Image account',
      media_kind: 'image',
      channel_ids: [21],
      configured_limit: 10,
      known_upstream_limit: null,
      effective_limit: 10,
      occupied: 3,
      unknown: 1,
      enabled: true,
      reconciled: true,
      available_slots: 7,
    },
  ],
}
function envelope(data: unknown = snapshot) {
  return { data: { success: true, data } }
}
function renderPanel() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  clients.push(client)
  return render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={client}>
        <MediaConcurrencyPanel active userId={1} />
      </QueryClientProvider>
    </I18nextProvider>
  )
}
beforeEach(async () => {
  await i18n.init({
    lng: 'en',
    fallbackLng: 'en',
    keySeparator: false,
    nsSeparator: false,
    resources: { en },
  })
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'root', role: ROLE.SUPER_ADMIN })
  vi.spyOn(api, 'get').mockResolvedValue(envelope())
})
afterEach(() => {
  cleanup()
  clients.splice(0).forEach((client) => client.clear())
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
})

it('administrators can read actual per-key limits but cannot edit or reconcile', async () => {
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'admin',
    role: ROLE.ADMIN,
    permissions: { admin_permissions: { media_operations: { read: true } } },
  })
  renderPanel()
  await screen.findByRole('table', { name: 'Media account admission pools' })
  expect(
    screen.getByLabelText('Default video concurrency per key')
  ).toBeDisabled()
  expect(screen.getByText('Enabled video admission limit: 16')).toBeVisible()
  expect(screen.getByText('Unknown', { exact: true })).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Save media concurrency' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Reconcile account pools' })
  ).not.toBeInTheDocument()
  expect(screen.queryByText('opaque-h3-1')).not.toBeInTheDocument()
  expect(screen.queryByText('opaque-historical')).not.toBeInTheDocument()
  const table = screen.getByRole('table', {
    name: 'Media account admission pools',
  })
  const row = within(table).getByRole('row', { name: /H3 account 1/ })
  expect(within(row).getByText('5', { exact: true })).toBeVisible()
})

it('invalid policy values remain visible and block confirmation rather than being clamped', async () => {
  const put = vi.spyOn(api, 'put')
  renderPanel()
  const input = await screen.findByLabelText(
    'Default video concurrency per key'
  )
  fireEvent.change(input, { target: { value: '17' } })
  await userEvent.click(
    screen.getByRole('button', { name: 'Save media concurrency' })
  )
  expect(
    await screen.findByText('Enter a whole number within the allowed range')
  ).toBeVisible()
  expect(input).toHaveValue('17')
  expect(input).toHaveAttribute('aria-invalid', 'true')
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  expect(put).not.toHaveBeenCalled()
})

it('root confirms an exact revisioned save, clears a blank override and preserves unlisted historical overrides', async () => {
  const initial = {
    ...snapshot,
    pool_overrides: [
      ...snapshot.pool_overrides,
      { pool_id: 'opaque-h3-1', limit: 2 },
    ],
  }
  vi.mocked(api.get).mockResolvedValue(envelope(initial))
  const put = vi.spyOn(api, 'put').mockResolvedValue(
    envelope({
      ...snapshot,
      revision: 'config-two',
      defaults: { video: 6, image: 12 },
    })
  )
  renderPanel()
  const video = await screen.findByLabelText(
    'Default video concurrency per key'
  )
  fireEvent.change(video, { target: { value: '6' } })
  fireEvent.change(screen.getByLabelText('Default image concurrency per key'), {
    target: { value: '12' },
  })
  fireEvent.change(screen.getByLabelText('Override for H3 account 1'), {
    target: { value: '' },
  })
  await userEvent.click(
    screen.getByRole('button', { name: 'Save media concurrency' })
  )
  expect(put).not.toHaveBeenCalled()
  const dialog = screen.getByRole('alertdialog')
  expect(
    within(dialog).getByText(/Running and UNKNOWN tasks are preserved/)
  ).toBeVisible()
  await userEvent.click(within(dialog).getByRole('button', { name: 'Confirm' }))
  await screen.findByText('Media concurrency saved')
  expect(put).toHaveBeenCalledExactlyOnceWith('/api/media-concurrency', {
    schema_version: 1,
    expected_revision: 'config-one',
    defaults: { video: 6, image: 12 },
    pool_overrides: [{ pool_id: 'opaque-historical', limit: 3 }],
  })
  expect(
    screen.getByLabelText('Default video concurrency per key')
  ).toHaveValue('6')
})

it('a 409 preserves the draft and blocks saving until an explicit reload obtains the current revision', async () => {
  const config = { headers: new AxiosHeaders() }
  const put = vi.spyOn(api, 'put').mockRejectedValue(
    new AxiosError('private body', 'ERR_BAD_REQUEST', config, undefined, {
      data: { message: 'private response' },
      status: 409,
      statusText: 'Conflict',
      headers: {},
      config,
    })
  )
  renderPanel()
  fireEvent.change(
    await screen.findByLabelText('Default video concurrency per key'),
    { target: { value: '3' } }
  )
  await userEvent.click(
    screen.getByRole('button', { name: 'Save media concurrency' })
  )
  await userEvent.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Confirm',
    })
  )
  await screen.findByText(
    'Media concurrency changed elsewhere. Reload the latest configuration before saving. Reloading discards this draft.'
  )
  expect(
    screen.getByLabelText('Default video concurrency per key')
  ).toHaveValue('3')
  expect(
    screen.getByRole('button', { name: 'Save media concurrency' })
  ).toBeDisabled()
  expect(put).toHaveBeenCalledOnce()
  expect(screen.queryByText('private response')).not.toBeInTheDocument()
  vi.mocked(api.get).mockResolvedValue(
    envelope({
      ...snapshot,
      revision: 'config-new',
      defaults: { video: 5, image: 10 },
    })
  )
  await userEvent.click(
    screen.getByRole('button', { name: 'Reload configuration' })
  )
  await waitFor(() =>
    expect(
      screen.getByLabelText('Default video concurrency per key')
    ).toHaveValue('5')
  )
  fireEvent.change(screen.getByLabelText('Default video concurrency per key'), {
    target: { value: '6' },
  })
  put.mockResolvedValueOnce(envelope({ ...snapshot, revision: 'config-final' }))
  await userEvent.click(
    screen.getByRole('button', { name: 'Save media concurrency' })
  )
  await userEvent.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Confirm',
    })
  )
  await waitFor(() => expect(put).toHaveBeenCalledTimes(2))
  expect(put.mock.lastCall?.[1]).toEqual(
    expect.objectContaining({ expected_revision: 'config-new' })
  )
})

it('failed business saves keep the draft and never show a saved message', async () => {
  vi.spyOn(api, 'put').mockResolvedValue({
    data: { success: false, message: 'private provider detail' },
  })
  renderPanel()
  fireEvent.change(
    await screen.findByLabelText('Default video concurrency per key'),
    { target: { value: '2' } }
  )
  await userEvent.click(
    screen.getByRole('button', { name: 'Save media concurrency' })
  )
  await userEvent.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Confirm',
    })
  )
  expect(
    await screen.findByText('Failed to save media concurrency')
  ).toBeVisible()
  expect(
    screen.getByLabelText('Default video concurrency per key')
  ).toHaveValue('2')
  expect(screen.queryByText('Media concurrency saved')).not.toBeInTheDocument()
  expect(screen.queryByText('private provider detail')).not.toBeInTheDocument()
})

it('root explicitly reconciles configured pools with schema one and receives a fresh ledger snapshot', async () => {
  const post = vi
    .spyOn(api, 'post')
    .mockResolvedValue(envelope({ ...snapshot, revision: 'reconciled' }))
  renderPanel()
  await screen.findByRole('table', { name: 'Media account admission pools' })
  await userEvent.click(
    screen.getByRole('button', { name: 'Reconcile account pools' })
  )
  expect(post).not.toHaveBeenCalled()
  const dialog = screen.getByRole('alertdialog')
  expect(
    within(dialog).getByText(/does not call providers or generate media/)
  ).toBeVisible()
  await userEvent.click(within(dialog).getByRole('button', { name: 'Confirm' }))
  await screen.findByText('Media account pools reconciled')
  expect(post).toHaveBeenCalledExactlyOnceWith(
    '/api/media-concurrency/refresh',
    { schema_version: 1 }
  )
})

it('a reconciliation failure preserves the existing snapshot and reports failure instead of success', async () => {
  vi.spyOn(api, 'post').mockRejectedValue(
    new Error('private credential detail')
  )
  renderPanel()
  await screen.findByRole('table', { name: 'Media account admission pools' })
  await userEvent.click(
    screen.getByRole('button', { name: 'Reconcile account pools' })
  )
  await userEvent.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Confirm',
    })
  )
  expect(
    await screen.findByText('Failed to reconcile media account pools')
  ).toBeVisible()
  expect(screen.getByText('H3 account 1')).toBeVisible()
  expect(
    screen.queryByText('Media account pools reconciled')
  ).not.toBeInTheDocument()
  expect(
    screen.queryByText('private credential detail')
  ).not.toBeInTheDocument()
})

it('an empty ledger shows no registered pools without inventing upstream availability', async () => {
  vi.mocked(api.get).mockResolvedValue(envelope({ ...snapshot, pools: [] }))
  renderPanel()
  expect(
    await screen.findByText('No registered media account pools')
  ).toBeVisible()
  expect(screen.queryByRole('table')).not.toBeInTheDocument()
  expect(
    screen.getByLabelText('Default video concurrency per key')
  ).toHaveValue('4')
})

it('paging account pools keeps edits on both pages in the confirmed request', async () => {
  const pools = Array.from({ length: 21 }, (_, index) => ({
    ...snapshot.pools[0],
    pool_id: `paging-${index + 1}`,
    label: `Page account ${index + 1}`,
  }))
  vi.mocked(api.get).mockResolvedValue(envelope({ ...snapshot, pools }))
  const put = vi.spyOn(api, 'put').mockResolvedValue(envelope())
  renderPanel()
  fireEvent.change(
    await screen.findByLabelText('Override for Page account 1'),
    { target: { value: '3' } }
  )
  expect(
    screen.queryByLabelText('Override for Page account 21')
  ).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Go to next page' }))
  fireEvent.change(
    await screen.findByLabelText('Override for Page account 21'),
    { target: { value: '2' } }
  )
  await userEvent.click(
    screen.getByRole('button', { name: 'Go to previous page' })
  )
  expect(
    await screen.findByLabelText('Override for Page account 1')
  ).toHaveValue('3')
  await userEvent.click(
    screen.getByRole('button', { name: 'Save media concurrency' })
  )
  await userEvent.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Confirm',
    })
  )
  await waitFor(() => expect(put).toHaveBeenCalledOnce())
  expect(put.mock.lastCall?.[1]).toEqual(
    expect.objectContaining({
      pool_overrides: [
        { pool_id: 'opaque-historical', limit: 3 },
        { pool_id: 'paging-1', limit: 3 },
        { pool_id: 'paging-21', limit: 2 },
      ],
    })
  )
})

it('load failures and malformed pool snapshots do not display invented zero capacity', async () => {
  vi.mocked(api.get).mockResolvedValue(
    envelope({
      ...snapshot,
      pools: [{ ...snapshot.pools[0], available_slots: null }],
    })
  )
  renderPanel()
  expect(
    await screen.findByText('Failed to load media concurrency')
  ).toBeVisible()
  expect(screen.queryByRole('table')).not.toBeInTheDocument()
  expect(
    screen.queryByText('Enabled video admission limit: 0')
  ).not.toBeInTheDocument()
})

it('losing root permission closes confirmation and losing admin permission removes all pool data', async () => {
  const put = vi.spyOn(api, 'put')
  renderPanel()
  fireEvent.change(
    await screen.findByLabelText('Default video concurrency per key'),
    { target: { value: '3' } }
  )
  await userEvent.click(
    screen.getByRole('button', { name: 'Save media concurrency' })
  )
  await act(async () => {
    useAuthStore.getState().auth.setUser({
      id: 1,
      username: 'admin',
      role: ROLE.ADMIN,
      permissions: {
        admin_permissions: { media_operations: { read: true } },
      },
    })
  })
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  expect(
    screen.getByLabelText('Default video concurrency per key')
  ).toBeDisabled()
  expect(put).not.toHaveBeenCalled()
  await act(async () => {
    useAuthStore.getState().auth.setUser({
      id: 2,
      username: 'other',
      role: ROLE.ADMIN,
      permissions: {
        admin_permissions: { media_operations: { read: true } },
      },
    })
  })
  expect(screen.getByText('Access denied')).toBeVisible()
  expect(screen.queryByText('H3 account 1')).not.toBeInTheDocument()
})

it('the configuration tab remains usable when the separate observation API fails', async () => {
  vi.mocked(api.get).mockImplementation((url) =>
    url === '/api/media-concurrency'
      ? Promise.resolve(envelope())
      : Promise.reject(new Error('private observation failure'))
  )
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={client}>
        <MediaOperationsPage />
      </QueryClientProvider>
    </I18nextProvider>
  )
  await screen.findByText('Failed to load media operations')
  await userEvent.click(
    screen.getByRole('tab', { name: 'Concurrency settings' })
  )
  expect(
    await screen.findByRole('table', { name: 'Media account admission pools' })
  ).toBeVisible()
})

it.each([
  ['en', en],
  ['zhCN', zh],
  ['zhTW', zhTW],
  ['fr', fr],
  ['ja', ja],
  ['ru', ru],
  ['vi', viLocale],
] as const)(
  'switching to %s translates policy and unknown capacity without changing numeric facts',
  async (language, resource) => {
    i18n.addResourceBundle(
      language,
      'translation',
      resource.translation,
      true,
      true
    )
    renderPanel()
    await screen.findByRole('table', { name: 'Media account admission pools' })
    await act(async () => {
      await i18n.changeLanguage(language)
    })
    const labels = resource.translation as Record<string, string>
    expect(labels['Known upstream limit']).toBeTruthy()
    expect(
      screen.getByRole('columnheader', { name: labels['Known upstream limit'] })
    ).toBeVisible()
    expect(
      screen.getByLabelText(labels['Default video concurrency per key'])
    ).toHaveValue('4')
    expect(screen.getByText(labels.Unknown, { exact: true })).toBeVisible()
  }
)
