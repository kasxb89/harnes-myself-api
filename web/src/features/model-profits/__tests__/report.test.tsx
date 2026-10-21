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
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import zh from '@/i18n/locales/zh.json'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { ModelProfitsPage } from '..'
import type { ModelProfitResult, ModelProfitRow } from '../types'

const partial: ModelProfitRow = {
  model_name: 'partial-model',
  requests: 2,
  revenue_cny: 20,
  known_cost_revenue_cny: 8,
  upstream_cost_cny: 5,
  gross_profit_cny: 3,
  profit_margin: 0.375,
  known_cost_requests: 1,
  unknown_cost_requests: 1,
  known_cost_events: 1,
  unknown_cost_events: 1,
  cost_coverage: 0.5,
  pending_requests: 0,
  pending_revenue_cny: 0,
  refund_cny: 0,
}
const unknown: ModelProfitRow = {
  ...partial,
  model_name: 'unknown-model',
  requests: 1,
  revenue_cny: 9,
  known_cost_revenue_cny: 0,
  upstream_cost_cny: null,
  gross_profit_cny: null,
  profit_margin: null,
  known_cost_requests: 0,
  known_cost_events: 0,
  cost_coverage: 0,
}
const payload: ModelProfitResult = {
  rows: [partial, unknown],
  summary: partial,
  timezone: 'Asia/Shanghai',
  currency: 'CNY',
  start_timestamp: 0,
  end_timestamp: 0,
}
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
})
afterEach(() => {
  cleanup()
  clients.splice(0).forEach((client) => client.clear())
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
})

function setup(
  options: {
    role?: number
    data?: ModelProfitResult
    failure?: boolean
    pending?: boolean
  } = {}
) {
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'admin',
    role: options.role ?? ROLE.ADMIN,
  })
  const get = vi.spyOn(api, 'get').mockImplementation(() => {
    if (options.pending) return new Promise(() => {})
    if (options.failure) {
      return Promise.resolve({
        data: { success: false, message: 'Unavailable' },
      })
    }
    return Promise.resolve({
      data: { success: true, data: options.data ?? payload },
    })
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={client}>
        <ModelProfitsPage />
      </QueryClientProvider>
    </I18nextProvider>
  )
  return get
}

it('labels incomplete coverage and displays only the server-known profit without inventing a zero cost', async () => {
  setup()
  const table = await screen.findByRole('table', { name: 'Profit by model' })
  const partialRow = within(table).getByRole('row', { name: /partial-model/ })
  expect(within(partialRow).getByText('CN¥3.00')).toBeVisible()
  expect(within(partialRow).getByText('CN¥8.00')).toBeVisible()
  expect(within(partialRow).getByText('50%')).toBeVisible()
  expect(within(partialRow).getByText('Partial')).toBeVisible()
  const unknownRow = within(table).getByRole('row', { name: /unknown-model/ })
  expect(within(unknownRow).getAllByText('Unknown')).toHaveLength(2)
  expect(within(unknownRow).getAllByText('CN¥0.00').length).toBeGreaterThan(0)
  expect(
    screen.getByText(/missing costs are never counted as zero/)
  ).toBeVisible()
})

it('requests an exact model and Shanghai inclusive date range only after Apply', async () => {
  const get = setup()
  await screen.findByText('partial-model')
  fireEvent.change(screen.getByLabelText('Start date'), {
    target: { value: '2026-10-20' },
  })
  fireEvent.change(screen.getByLabelText('End date (inclusive)'), {
    target: { value: '2026-10-21' },
  })
  fireEvent.change(screen.getByLabelText('Exact model name'), {
    target: { value: ' exact-model ' },
  })
  expect(get).toHaveBeenCalledTimes(1)
  fireEvent.click(screen.getByRole('button', { name: 'Apply filters' }))
  await waitFor(() =>
    expect(get).toHaveBeenLastCalledWith('/api/model-profit/', {
      params: {
        start_timestamp: Date.parse('2026-10-20T00:00:00+08:00') / 1000,
        end_timestamp: Date.parse('2026-10-22T00:00:00+08:00') / 1000,
        model_name: 'exact-model',
      },
    })
  )
})

it('blocks reversed date submission and preserves the last applied result', async () => {
  const get = setup()
  await screen.findByText('partial-model')
  fireEvent.change(screen.getByLabelText('Start date'), {
    target: { value: '2026-10-22' },
  })
  fireEvent.change(screen.getByLabelText('End date (inclusive)'), {
    target: { value: '2026-10-21' },
  })
  expect(screen.getByRole('button', { name: 'Apply filters' })).toBeDisabled()
  expect(screen.getByLabelText('End date (inclusive)')).toHaveAttribute(
    'aria-invalid',
    'true'
  )
  expect(get).toHaveBeenCalledTimes(1)
  expect(screen.getByText('partial-model')).toBeVisible()
})

it('shows an empty period without making up model rows', async () => {
  setup({ data: { ...payload, rows: [] } })
  expect(
    await screen.findByText('No model consumption in this period')
  ).toBeVisible()
  expect(screen.queryByRole('table')).not.toBeInTheDocument()
})

it('warns that disabled consumption logging prevents complete profit reporting', async () => {
  setup({ data: { ...payload, consumption_logging_enabled: false } })
  expect(
    await screen.findByText(/Consumption logging is disabled/)
  ).toBeVisible()
})

it('keeps wide financial columns in a horizontal scroll area and aligns amounts consistently', async () => {
  setup({
    data: {
      ...payload,
      rows: [{ ...partial, model_name: 'very-long-model-'.repeat(15) }],
    },
  })
  const table = await screen.findByRole('table', { name: 'Profit by model' })
  expect(table.parentElement).toHaveClass('overflow-x-auto')
  const row = within(table).getByRole('row', { name: /very-long-model-/ })
  expect(within(row).getByRole('cell', { name: 'CN¥3.00' })).toHaveClass(
    'text-right'
  )
})

it('shows request failure with Retry and does not expose stale profit figures', async () => {
  const get = setup({ failure: true })
  expect(await screen.findByText('Failed to load model profits')).toBeVisible()
  expect(
    screen.queryByRole('region', { name: 'Model profit summary' })
  ).not.toBeInTheDocument()
  get.mockResolvedValue({ data: { success: true, data: payload } })
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(await screen.findByText('partial-model')).toBeVisible()
})

it('announces loading and disables reloading actions while the request is pending', () => {
  setup({ pending: true })
  expect(screen.getByRole('status')).toHaveTextContent(
    'Loading model profits...'
  )
  expect(screen.getByRole('button', { name: 'Refresh' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Apply filters' })).toBeDisabled()
})

it('does not fetch profit or cost data for ordinary users', () => {
  const get = setup({ role: ROLE.USER })
  expect(screen.getByText('Access denied')).toBeVisible()
  expect(get).not.toHaveBeenCalled()
})

it('keeps supplier editing hidden from administrators below root', async () => {
  setup()
  await screen.findByText('partial-model')
  expect(
    screen.queryByRole('button', { name: 'Configure upstream costs' })
  ).not.toBeInTheDocument()
})

it('updates amounts and labels immediately when the language switches', async () => {
  setup()
  await screen.findByText('partial-model')
  await act(() => i18n.changeLanguage('zhCN'))
  expect(screen.getByRole('heading', { name: '模型利润' })).toBeVisible()
  expect(within(screen.getByRole('table')).getByText('¥3.00')).toBeVisible()
})
