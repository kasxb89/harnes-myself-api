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
import { Route } from '@/routes/_authenticated/media-operations'
import { useAuthStore } from '@/stores/auth-store'

import { MediaOperationsPage } from '..'
import { emptyMediaPayload, mediaPayload } from './fixtures'

const clients: QueryClient[] = []
const i18n = createInstance()
beforeEach(async () => {
  await i18n.init({
    lng: 'en',
    fallbackLng: 'en',
    keySeparator: false,
    nsSeparator: false,
    resources: { en, zhCN: zh },
  })
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'admin', role: ROLE.ADMIN })
})
afterEach(() => {
  cleanup()
  clients.splice(0).forEach((client) => client.clear())
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
})
function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  return render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={client}>
        <MediaOperationsPage />
      </QueryClientProvider>
    </I18nextProvider>
  )
}
function mockResult(data: unknown = mediaPayload) {
  return vi
    .spyOn(api, 'get')
    .mockResolvedValue({ data: { success: true, data } })
}

it('ordinary users are denied by both the route and the query gate', () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 2, username: 'user', role: ROLE.USER })
  const get = mockResult()
  expect(() => (Route.options.beforeLoad as () => void)()).toThrow()
  renderPage()
  expect(screen.getByText('Access denied')).toBeVisible()
  expect(get).not.toHaveBeenCalled()
})

it('current records retain unknown capacity without inventing free slots or percentages', async () => {
  mockResult({
    ...mediaPayload,
    private_prompt: 'private customer content',
    api_key: 'private credential',
  })
  renderPage()
  const snapshot = await screen.findByRole('region', {
    name: 'Current task snapshot',
  })
  expect(within(snapshot).getByText('Unknown', { exact: true })).toBeVisible()
  expect(within(snapshot).getByText('7', { exact: true })).toBeVisible()
  const table = screen.getByRole('table', {
    name: 'Current media task records',
  })
  const row = within(table).getByRole('row', { name: /MiniMax-H3/ })
  expect(
    within(row)
      .getAllByRole('cell')
      .map((cell) => cell.textContent)
  ).toEqual(['MiniMax-H3', 'Video', '1', '3', '2', '4', '5', 'Unknown'])
  expect(screen.queryByText('0%')).not.toBeInTheDocument()
  expect(screen.queryByText('private customer content')).not.toBeInTheDocument()
  expect(screen.queryByText('private credential')).not.toBeInTheDocument()
})

it('history separates deduplicated requests from events, policy attempts and client evidence', async () => {
  mockResult()
  renderPage()
  await screen.findByRole('table', { name: 'Current media task records' })
  await userEvent.click(
    screen.getByRole('tab', { name: 'Historical observations' })
  )
  const table = await screen.findByRole('table', {
    name: 'Media history by model',
  })
  const row = within(table).getByRole('row', { name: /MiniMax-H3/ })
  expect(
    within(row)
      .getAllByRole('cell')
      .map((cell) => cell.textContent)
  ).toEqual(['MiniMax-H3', '19', '2', '8', '3', '4'])
  expect(
    screen.getByRole('columnheader', { name: 'Recorded policy attempts' })
  ).toBeVisible()
  expect(
    screen.getByRole('table', { name: 'Observed error categories' })
  ).toHaveTextContent('Content policy')
  expect(
    screen.getByRole('table', { name: 'Observed error categories' })
  ).toHaveTextContent('Upstream generation resources')
  const stages = screen.getByRole('table', { name: 'Observation stages' })
  expect(stages).toHaveTextContent('Gateway record')
  expect(stages).toHaveTextContent('Client report')
  expect(stages).toHaveTextContent('Preflight')
})

it('polling counters are in a separate keyboard-accessible tab with their consecutive meaning', async () => {
  mockResult()
  renderPage()
  await screen.findByRole('table', { name: 'Current media task records' })
  const current = screen.getByRole('tab', { name: 'Current task records' })
  current.focus()
  await userEvent.keyboard('{End}')
  const poll = screen.getByRole('tab', { name: 'Polling diagnostics' })
  expect(poll).toHaveFocus()
  await userEvent.keyboard('{Enter}')
  expect(poll).toHaveAttribute('aria-selected', 'true')
  const table = screen.getByRole('table', { name: 'Media polling diagnostics' })
  expect(
    within(table).getByRole('columnheader', {
      name: 'Maximum consecutive polling failures',
    })
  ).toBeVisible()
  expect(
    screen.queryByRole('table', { name: 'Media history by model' })
  ).not.toBeInTheDocument()
})

it('pending and failed requests cannot masquerade as an empty successful report', async () => {
  const get = vi
    .spyOn(api, 'get')
    .mockImplementationOnce(() => new Promise(() => {}))
  const rendered = renderPage()
  expect(
    screen.getByRole('status', { name: 'Loading media operations' })
  ).toBeVisible()
  expect(screen.queryByText('No recorded media models')).not.toBeInTheDocument()
  rendered.unmount()
  clients.splice(0).forEach((client) => client.clear())
  get.mockRejectedValueOnce(new Error('private upstream failure'))
  renderPage()
  expect(
    await screen.findByText('Failed to load media operations')
  ).toBeVisible()
  expect(screen.queryByText('private upstream failure')).not.toBeInTheDocument()
  expect(screen.queryByText('No recorded media models')).not.toBeInTheDocument()
  get.mockResolvedValueOnce({
    data: { success: true, data: emptyMediaPayload },
  })
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(await screen.findByText('No recorded media models')).toBeVisible()
})

it('malformed capacity is rejected rather than displayed as a known zero', async () => {
  mockResult({
    ...mediaPayload,
    models: [
      {
        ...mediaPayload.models[0],
        capacity: {
          status: 'unknown',
          configured_limit: 0,
          available_slots: 0,
        },
      },
    ],
  })
  renderPage()
  expect(
    await screen.findByText('Failed to load media operations')
  ).toBeVisible()
  expect(
    screen.queryByRole('table', { name: 'Current media task records' })
  ).not.toBeInTheDocument()
})

it('filter choices apply only on demand and retain an exact model after an empty result', async () => {
  const get = mockResult()
  renderPage()
  await screen.findByRole('table', { name: 'Current media task records' })
  const user = userEvent.setup()
  await user.click(screen.getByRole('combobox', { name: 'Media type' }))
  await user.click(await screen.findByRole('option', { name: 'Video' }))
  await user.click(screen.getByRole('combobox', { name: 'Model' }))
  await user.click(await screen.findByRole('option', { name: 'MiniMax-H3' }))
  expect(get).toHaveBeenCalledOnce()
  get.mockResolvedValueOnce({
    data: { success: true, data: emptyMediaPayload },
  })
  await user.click(screen.getByRole('button', { name: 'Apply filters' }))
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith('/api/media-operations', {
      params: expect.objectContaining({
        media_kind: 'video',
        model: 'MiniMax-H3',
      }),
    })
  )
  await screen.findByText('No recorded media models')
  expect(screen.getByRole('combobox', { name: 'Model' })).toHaveValue(
    'MiniMax-H3'
  )
})

it('an invalid range blocks applying and a today preset never sends a future end', async () => {
  const get = mockResult()
  renderPage()
  await screen.findByRole('table', { name: 'Current media task records' })
  const user = userEvent.setup()
  const dateGroup = screen.getByRole('group', { name: 'History period' })
  await user.click(within(dateGroup).getByRole('button'))
  fireEvent.change(screen.getByLabelText('Start Time'), {
    target: { value: '2026-01-01T00:00' },
  })
  fireEvent.change(screen.getByLabelText('End Time'), {
    target: { value: '2026-03-01T00:00' },
  })
  await user.click(screen.getByRole('button', { name: 'Confirm' }))
  expect(screen.getByRole('button', { name: 'Apply filters' })).toBeDisabled()
  expect(
    screen.getByText('Choose a valid past time range of up to 31 days.')
  ).toBeVisible()
  expect(get).toHaveBeenCalledOnce()
  await user.click(within(dateGroup).getByRole('button'))
  await user.click(screen.getByRole('button', { name: 'Today' }))
  await user.click(screen.getByRole('button', { name: 'Apply filters' }))
  await waitFor(() => expect(get).toHaveBeenCalledTimes(2))
  expect(get.mock.lastCall?.[1]?.params.end_timestamp).toBeLessThanOrEqual(
    Math.floor(Date.now() / 1000)
  )
})

it('losing administrator permission removes previously loaded operational data', async () => {
  const get = mockResult()
  renderPage()
  await screen.findByRole('table', { name: 'Current media task records' })
  await act(async () => {
    useAuthStore
      .getState()
      .auth.setUser({ id: 1, username: 'admin', role: ROLE.USER })
  })
  expect(screen.getByText('Access denied')).toBeVisible()
  expect(screen.queryByText('MiniMax-H3')).not.toBeInTheDocument()
  expect(get).toHaveBeenCalledOnce()
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
  'switching to %s translates the live page, category and unknown capacity',
  async (language, resources) => {
    const labels = resources.translation as Record<string, string>
    expect(labels['Image and video operations']).toBeTruthy()
    expect(labels['Configured concurrency']).toBeTruthy()
    expect(labels['Content policy']).toBeTruthy()
    i18n.addResourceBundle(
      language,
      'translation',
      resources.translation,
      true,
      true
    )
    mockResult()
    renderPage()
    await screen.findByRole('table', { name: 'Current media task records' })
    await act(async () => {
      await i18n.changeLanguage(language)
    })
    expect(screen.getByText(labels['Image and video operations'])).toBeVisible()
    const snapshot = screen.getByRole('region', {
      name: labels['Current task snapshot'],
    })
    expect(
      within(snapshot).getByText(labels['Configured concurrency'])
    ).toBeVisible()
    expect(
      within(snapshot).getByText(labels.Unknown, { exact: true })
    ).toBeVisible()
    await userEvent.click(
      screen.getByRole('tab', { name: labels['Historical observations'] })
    )
    const categoryTable = screen.getByRole('table', {
      name: labels['Observed error categories'],
    })
    expect(
      within(categoryTable).getByText(labels['Content policy'])
    ).toBeVisible()
  }
)

it('narrowing to one model keeps other previously loaded models selectable without another directory query', async () => {
  const get = mockResult()
  renderPage()
  await screen.findByRole('table', { name: 'Current media task records' })
  const user = userEvent.setup()
  await user.click(screen.getByRole('combobox', { name: 'Model' }))
  await user.click(await screen.findByRole('option', { name: 'MiniMax-H3' }))
  get.mockResolvedValueOnce({
    data: {
      success: true,
      data: { ...mediaPayload, models: [mediaPayload.models[0]] },
    },
  })
  await user.click(screen.getByRole('button', { name: 'Apply filters' }))
  await waitFor(() => expect(get).toHaveBeenCalledTimes(2))
  await waitFor(() =>
    expect(
      screen.queryByText('yunshu-internal-gpt-image-2')
    ).not.toBeInTheDocument()
  )
  await user.click(screen.getByRole('combobox', { name: 'Model' }))
  expect(
    await screen.findByRole('option', { name: 'yunshu-internal-gpt-image-2' })
  ).toBeVisible()
  expect(get).toHaveBeenCalledTimes(2)
})

it('a confirmed broad-query rejection tells the administrator to narrow filters instead of blindly retrying', async () => {
  const config = { headers: new AxiosHeaders() }
  vi.spyOn(api, 'get').mockRejectedValue(
    new AxiosError(
      'private database details',
      'ERR_BAD_REQUEST',
      config,
      undefined,
      {
        data: {
          success: false,
          code: 'MEDIA_OPERATIONS_QUERY_TOO_BROAD',
          message: 'untrusted private response',
        },
        status: 422,
        statusText: 'Unprocessable Entity',
        headers: {},
        config,
      }
    )
  )
  renderPage()
  expect(
    await screen.findByText('Narrow the time range or choose a model')
  ).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Retry' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByText('untrusted private response')
  ).not.toBeInTheDocument()
  expect(screen.queryByText('private database details')).not.toBeInTheDocument()
})

it.each([
  { status: 500, code: 'MEDIA_OPERATIONS_QUERY_TOO_BROAD' },
  { status: 422, code: 'MEDIA_OPERATIONS_QUERY_FAILED' },
])(
  'status $status with code $code keeps the generic safe error',
  async ({ status, code }) => {
    const config = { headers: new AxiosHeaders() }
    vi.spyOn(api, 'get').mockRejectedValue(
      new AxiosError(
        'private database details',
        'ERR_BAD_REQUEST',
        config,
        undefined,
        {
          data: { success: false, code, message: 'untrusted private response' },
          status,
          statusText: 'Error',
          headers: {},
          config,
        }
      )
    )
    renderPage()
    expect(
      await screen.findByText('Failed to load media operations')
    ).toBeVisible()
    expect(screen.getByRole('button', { name: 'Retry' })).toBeVisible()
    expect(
      screen.queryByText('Narrow the time range or choose a model')
    ).not.toBeInTheDocument()
    expect(
      screen.queryByText('untrusted private response')
    ).not.toBeInTheDocument()
  }
)

it('switching administrator accounts resets filters and the accumulated model directory', async () => {
  const get = mockResult()
  renderPage()
  await screen.findByRole('table', { name: 'Current media task records' })
  get.mockResolvedValueOnce({
    data: { success: true, data: emptyMediaPayload },
  })
  await act(async () => {
    useAuthStore
      .getState()
      .auth.setUser({ id: 8, username: 'other-admin', role: ROLE.ADMIN })
  })
  await screen.findByText('No recorded media models')
  const user = userEvent.setup()
  await user.click(screen.getByRole('combobox', { name: 'Model' }))
  expect(
    await screen.findByRole('option', { name: 'All models' })
  ).toBeVisible()
  expect(
    screen.queryByRole('option', { name: 'MiniMax-H3' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('option', { name: 'yunshu-internal-gpt-image-2' })
  ).not.toBeInTheDocument()
  expect(get.mock.lastCall?.[1]?.params.model).toBeUndefined()
})
