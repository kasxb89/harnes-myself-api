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
import { readFileSync } from 'node:fs'
import path from 'node:path'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  act,
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { YcapiCatalog } from '..'

const catalog = JSON.parse(
  readFileSync(path.resolve(process.cwd(), '../pkg/ycapi/catalog.json'), 'utf8')
)
const confirmedQuotes = JSON.parse(
  readFileSync(
    path.resolve(process.cwd(), '../pkg/modelpricebook/approved_quotes.json'),
    'utf8'
  )
).quotes
const clients: QueryClient[] = []
afterEach(() => {
  cleanup()
  clients.splice(0).forEach((client) => client.clear())
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
})
it('includes the original Metaso H3 channel beside the separate YCAPI H3 candidate', async () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'root', role: 100 })
  vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data:
        url === '/api/model-pricebook'
          ? {
              schema_version: 1,
              generated_at: 1793500000,
              usd_exchange_rate: 7.3,
              rows: [
                {
                  model_name: 'MiniMax-H3',
                  state: 'enabled',
                  approved_quote: confirmedQuotes.find(
                    (q: { model_name: string }) => q.model_name === 'MiniMax-H3'
                  ),
                  channel_id: 16,
                  channel_name: 'Metaso H3',
                  channel_type: 35,
                  upstream_host: 'metaso.cn',
                  channel_status: 1,
                  enabled_groups: ['default'],
                  pricing: null,
                  cost_rule: null,
                },
              ],
            }
          : catalog,
    },
  }))
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <YcapiCatalog />
    </QueryClientProvider>
  )
  await waitFor(() => expect(screen.getByText('MiniMax-H3')).toBeVisible())
  expect(screen.getByText('metaso.cn')).toBeVisible()
  expect(screen.getByText(/Verified public reference estimate/)).toBeVisible()
  expect(screen.getByText('Not yet effective')).toBeVisible()
  expect(
    screen.queryByText('No confirmed quote recorded')
  ).not.toBeInTheDocument()
  await userEvent.setup().click(
    screen.getByRole('button', {
      name: 'View confirmed specification prices',
    })
  )
  const dialog = within(await screen.findByRole('dialog'))
  expect(dialog.getByText(/0\.0897396$/)).toBeVisible()
  expect(dialog.getByText(/0\.149566$/)).toBeVisible()
  await userEvent.setup().keyboard('{Escape}')
  await userEvent
    .setup()
    .type(screen.getByRole('textbox', { name: 'Search prices' }), 'MiniMax H3')
  expect(screen.getAllByText('MiniMax H3').length).toBeGreaterThan(0)
})

it('keeps the new approved price table usable when current channel prices fail and allows retry', async () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'root', role: 100 })
  let attempts = 0
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/model-pricebook') {
      attempts++
      return {
        data:
          attempts === 1
            ? { success: false, message: 'Unavailable' }
            : {
                success: true,
                data: {
                  schema_version: 1,
                  generated_at: 1793500000,
                  usd_exchange_rate: 7.3,
                  rows: [],
                },
              },
      }
    }
    return { data: { success: true, data: catalog } }
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <YcapiCatalog />
    </QueryClientProvider>
  )
  await screen.findByText('Failed to load configured model prices')
  expect(
    screen.getByRole('region', { name: 'New YCAPI candidate prices' })
  ).toBeVisible()
  await userEvent.setup().click(screen.getByRole('button', { name: 'Retry' }))
  await waitFor(() =>
    expect(
      screen.queryByText('Failed to load configured model prices')
    ).not.toBeInTheDocument()
  )
  expect(attempts).toBe(2)
})
it('does not request cost data for ordinary users and hides an existing root view immediately after role revocation', async () => {
  useAuthStore.getState().auth.setUser({ id: 7, username: 'user', role: 1 })
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValue({ data: { success: true, data: catalog } })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <YcapiCatalog />
    </QueryClientProvider>
  )
  expect(screen.getByText('Access denied')).toBeVisible()
  expect(get).not.toHaveBeenCalled()
  await act(async () =>
    useAuthStore.getState().auth.setUser({ id: 7, username: 'root', role: 100 })
  )
  await screen.findByText('Approved model price list')
  await act(async () =>
    useAuthStore.getState().auth.setUser({ id: 7, username: 'user', role: 1 })
  )
  expect(screen.getByText('Access denied')).toBeVisible()
  expect(
    screen.queryByRole('region', { name: 'New YCAPI candidate prices' })
  ).not.toBeInTheDocument()
})

it('uses the selected group ratio and preserves a configured zero-priced group', async () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'root', role: 100 })
  const row = {
    model_name: 'legacy-text',
    state: 'enabled',
    channel_id: 12,
    channel_name: 'Legacy text channel',
    channel_type: 1,
    upstream_host: 'example.invalid',
    channel_status: 1,
    enabled_groups: ['default', 'free'],
    cost_rule: null,
    pricing: {
      model_name: 'legacy-text',
      id: 0,
      quota_type: 0,
      model_ratio: 1,
      completion_ratio: 2,
      enable_groups: ['default', 'free'],
    },
  }
  vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data:
        url === '/api/model-pricebook'
          ? {
              schema_version: 1,
              generated_at: 1793500000,
              usd_exchange_rate: 7.3,
              group_ratios: { default: 1, free: 0 },
              rows: [row],
            }
          : catalog,
    },
  }))
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <YcapiCatalog />
    </QueryClientProvider>
  )
  await waitFor(() => expect(screen.getByText('legacy-text')).toBeVisible())
  await userEvent
    .setup()
    .selectOptions(
      screen.getByRole('combobox', { name: 'Price group' }),
      'free'
    )
  await waitFor(() =>
    expect(
      within(
        screen.getByRole('region', { name: 'Current configured models' })
      ).getAllByText('0')
    ).toHaveLength(2)
  )
})

it('shows enabled, disabled and unrouted candidates by default and filters without removing their confirmed quotes', async () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'root', role: 100 })
  const base = {
    channel_id: 0,
    channel_name: '',
    channel_type: 0,
    upstream_host: '',
    channel_status: 0,
    enabled_groups: [],
    pricing: null,
    cost_rule: null,
  }
  const rows = [
    {
      ...base,
      model_name: 'running-original',
      channel_id: 16,
      channel_status: 1,
      enabled_groups: ['default'],
      state: 'enabled',
    },
    {
      ...base,
      model_name: 'disabled-original',
      channel_id: 17,
      channel_status: 2,
      state: 'disabled',
    },
    {
      ...base,
      model_name: 'ycapi-gpt-6.1-sol',
      state: 'not_configured',
      approved_quote: confirmedQuotes.find(
        (q: { model_name: string }) => q.model_name === 'ycapi-gpt-6.1-sol'
      ),
    },
  ]
  vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data:
        url === '/api/model-pricebook'
          ? {
              schema_version: 1,
              generated_at: 1793500000,
              usd_exchange_rate: 7.3,
              rows,
            }
          : catalog,
    },
  }))
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <YcapiCatalog />
    </QueryClientProvider>
  )
  const region = within(
    await screen.findByRole('region', { name: 'Current configured models' })
  )
  await waitFor(() =>
    expect(region.getByText('running-original')).toBeVisible()
  )
  expect(region.getByText('disabled-original')).toBeVisible()
  expect(region.getByText('ycapi-gpt-6.1-sol')).toBeVisible()
  expect(region.getByText(/Confirmed procurement quote/)).toBeVisible()
  await userEvent
    .setup()
    .selectOptions(
      region.getByRole('combobox', { name: 'Model activation status' }),
      'not_enabled'
    )
  expect(region.queryByText('running-original')).not.toBeInTheDocument()
  expect(region.getByText('disabled-original')).toBeVisible()
  expect(region.getByText(/Confirmed procurement quote/)).toBeVisible()
  await userEvent
    .setup()
    .selectOptions(
      region.getByRole('combobox', { name: 'Model activation status' }),
      'enabled'
    )
  expect(region.getByText('running-original')).toBeVisible()
  expect(region.queryByText('disabled-original')).not.toBeInTheDocument()
})
