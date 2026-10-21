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
  model_options: [
    'partial-model',
    'unknown-model',
    'exact-model',
    'exact-model-extended',
    'retired-model',
  ],
  trend_granularity: 'day',
  trend: [
    {
      ...partial,
      bucket_start: Date.parse('2026-10-20T00:00:00+08:00') / 1000,
    },
    {
      ...unknown,
      bucket_start: Date.parse('2026-10-21T00:00:00+08:00') / 1000,
    },
  ],
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

it('keeps exactly four detail columns and labels known-cost profit without counting unknown costs as zero', async () => {
  setup()
  const table = await screen.findByRole('table', { name: 'Profit by model' })
  expect(
    within(table)
      .getAllByRole('columnheader')
      .map((cell) => cell.textContent)
  ).toEqual(['Model', 'Requests', 'Billed revenue', 'Revenue with known costs'])
  const partialRow = within(table).getByRole('row', { name: /partial-model/ })
  expect(within(partialRow).getByText('CN¥8.00')).toBeVisible()
  const unknownRow = within(table).getByRole('row', { name: /unknown-model/ })
  expect(within(unknownRow).getByText('CN¥0.00')).toBeVisible()
  const summary = screen.getByRole('region', { name: 'Model profit summary' })
  expect(
    within(summary).getByText('Gross profit (known-cost portion)')
  ).toBeVisible()
  expect(within(summary).getByText('CN¥3.00')).toBeVisible()
  expect(within(summary).getByText('50%')).toBeVisible()
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
  const user = userEvent.setup()
  await user.click(screen.getByRole('combobox', { name: 'Model' }))
  await user.type(
    screen.getByRole('combobox', { name: 'Model' }),
    'exact-model'
  )
  await user.click(screen.getByRole('option', { name: 'exact-model' }))
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
  expect(within(row).getByRole('cell', { name: 'CN¥8.00' })).toHaveClass(
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
  expect(within(screen.getByRole('table')).getByText('¥8.00')).toBeVisible()
})

it('offers historical models and clears selection using All models without accepting arbitrary search text', async () => {
  const get = setup()
  await screen.findByText('partial-model')
  const user = userEvent.setup()
  const model = screen.getByRole('combobox', { name: 'Model' })
  await user.click(model)
  await user.type(model, 'retired')
  await user.click(screen.getByRole('option', { name: 'retired-model' }))
  fireEvent.click(screen.getByRole('button', { name: 'Apply filters' }))
  await waitFor(() =>
    expect(get.mock.lastCall?.[1]).toMatchObject({
      params: { model_name: 'retired-model' },
    })
  )
  await user.click(model)
  await user.type(model, 'does-not-exist')
  expect(screen.getByText('No matching models')).toBeVisible()
  await user.keyboard('{Escape}')
  expect(model).toHaveValue('retired-model')
  await user.click(model)
  await user.click(screen.getByRole('option', { name: 'All models' }))
  fireEvent.click(screen.getByRole('button', { name: 'Apply filters' }))
  await waitFor(() =>
    expect(get.mock.lastCall?.[1]).toMatchObject({
      params: { model_name: undefined },
    })
  )
})

it('switches to the dashboard using the same applied data and shows actual daily values', async () => {
  const get = setup()
  await screen.findByText('partial-model')
  fireEvent.click(screen.getByRole('tab', { name: 'Profit dashboard' }))
  const panel = screen.getByRole('tabpanel', { name: 'Profit dashboard' })
  expect(within(panel).getByText('Model revenue ranking')).toBeVisible()
  expect(within(panel).getByText('Request distribution')).toBeVisible()
  expect(within(panel).getByText('Daily revenue and requests')).toBeVisible()
  const daily = within(panel).getByRole('list', {
    name: 'Daily trend values',
    hidden: true,
  })
  expect(daily).toHaveTextContent('10/20/2026')
  expect(daily).toHaveTextContent('CN¥20.00')
  expect(daily).toHaveTextContent('CN¥9.00')
  expect(screen.queryByRole('table')).not.toBeInTheDocument()
  expect(get).toHaveBeenCalledTimes(1)
  fireEvent.click(screen.getByRole('tab', { name: 'Profit details' }))
  expect(screen.getByRole('table', { name: 'Profit by model' })).toBeVisible()
})

it('shows an empty dashboard instead of invented charts', async () => {
  setup({ data: { ...payload, rows: [], trend: [] } })
  await screen.findByText('No model consumption in this period')
  fireEvent.click(screen.getByRole('tab', { name: 'Profit dashboard' }))
  expect(screen.getByRole('tabpanel')).toHaveTextContent(
    'No model consumption in this period'
  )
  expect(
    screen.queryByRole('img', { name: 'Model revenue ranking' })
  ).not.toBeInTheDocument()
})

it('does not fabricate a time series when an older response has no daily data', async () => {
  setup({ data: { ...payload, trend: undefined } })
  await screen.findByText('partial-model')
  fireEvent.click(screen.getByRole('tab', { name: 'Profit dashboard' }))
  expect(
    screen.getByText('Daily trend is unavailable for this report')
  ).toBeVisible()
})

it('keeps all-unknown summary amounts unknown in the dashboard', async () => {
  setup({ data: { ...payload, rows: [unknown], summary: unknown } })
  await screen.findByText('unknown-model')
  fireEvent.click(screen.getByRole('tab', { name: 'Profit dashboard' }))
  const summary = screen.getByRole('region', { name: 'Model profit summary' })
  expect(within(summary).getAllByText('—')).toHaveLength(3)
  expect(
    within(summary).queryByText('CN¥9.00', { selector: '.text-destructive' })
  ).not.toBeInTheDocument()
  expect(
    screen.getByText(/missing costs are never counted as zero/)
  ).toBeVisible()
})

it('keeps a failed dashboard free of stale charts and supports Retry', async () => {
  const get = setup({ failure: true })
  await screen.findByText('Failed to load model profits')
  fireEvent.click(screen.getByRole('tab', { name: 'Profit dashboard' }))
  expect(
    screen.queryByRole('img', { name: 'Model revenue ranking' })
  ).not.toBeInTheDocument()
  get.mockResolvedValue({ data: { success: true, data: payload } })
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(
    await screen.findByRole('img', { name: 'Model revenue ranking' })
  ).toBeVisible()
})

it('keeps dropdown popups inside fullscreen and follows native exit events', async () => {
  const descriptors = [
    'fullscreenEnabled',
    'fullscreenElement',
    'exitFullscreen',
  ].map((key) => [key, Object.getOwnPropertyDescriptor(document, key)] as const)
  const enterDescriptor = Object.getOwnPropertyDescriptor(
    HTMLElement.prototype,
    'requestFullscreen'
  )
  Object.defineProperty(document, 'fullscreenEnabled', {
    configurable: true,
    value: true,
  })
  Object.defineProperty(document, 'fullscreenElement', {
    configurable: true,
    writable: true,
    value: null,
  })
  Object.defineProperty(HTMLElement.prototype, 'requestFullscreen', {
    configurable: true,
    value: vi.fn(function (this: HTMLElement) {
      Object.defineProperty(document, 'fullscreenElement', {
        configurable: true,
        writable: true,
        value: this,
      })
      document.dispatchEvent(new Event('fullscreenchange'))
      return Promise.resolve()
    }),
  })
  Object.defineProperty(document, 'exitFullscreen', {
    configurable: true,
    value: vi.fn(() => {
      Object.defineProperty(document, 'fullscreenElement', {
        configurable: true,
        writable: true,
        value: null,
      })
      document.dispatchEvent(new Event('fullscreenchange'))
      return Promise.resolve()
    }),
  })
  try {
    setup()
    await screen.findByText('partial-model')
    const user = userEvent.setup()
    await user.click(screen.getByRole('tab', { name: 'Profit dashboard' }))
    await user.click(screen.getByRole('button', { name: 'Enter fullscreen' }))
    expect(
      screen.getByRole('button', { name: 'Exit fullscreen' })
    ).toHaveAttribute('aria-pressed', 'true')
    await user.click(screen.getByText('Filters', { selector: 'summary' }))
    await user.click(screen.getByRole('combobox', { name: 'Model' }))
    expect(document.fullscreenElement).toContainElement(
      screen.getByRole('listbox')
    )
    await user.keyboard('{Escape}')
    await user.click(screen.getByRole('button', { name: 'Exit fullscreen' }))
    expect(
      screen.getByRole('button', { name: 'Enter fullscreen' })
    ).toHaveAttribute('aria-pressed', 'false')
  } finally {
    for (const [key, descriptor] of descriptors) {
      if (descriptor) Object.defineProperty(document, key, descriptor)
      else Reflect.deleteProperty(document, key)
    }
    if (enterDescriptor) {
      Object.defineProperty(
        HTMLElement.prototype,
        'requestFullscreen',
        enterDescriptor
      )
    } else {
      Reflect.deleteProperty(HTMLElement.prototype, 'requestFullscreen')
    }
  }
})

it('includes pending reserved charges in displayed revenue while labeling profit as the known-cost portion', async () => {
  const pending = {
    ...partial,
    requests: 3,
    revenue_cny: 27,
    pending_requests: 1,
    pending_revenue_cny: 7,
  }
  setup({
    data: {
      ...payload,
      rows: [pending],
      summary: pending,
      trend: [
        {
          ...pending,
          bucket_start: Date.parse('2026-10-20T00:00:00+08:00') / 1000,
        },
      ],
    },
  })
  await screen.findByText('partial-model')
  fireEvent.click(screen.getByRole('tab', { name: 'Profit dashboard' }))
  const panel = screen.getByRole('tabpanel', { name: 'Profit dashboard' })
  expect(
    within(panel).getByText(
      'Shanghai calendar days; revenue and requests use separate axes. Revenue includes pending reserved charges; gross profit covers only the known-cost portion.'
    )
  ).toBeVisible()
  expect(
    within(panel).getByRole('list', {
      name: 'Daily trend values',
      hidden: true,
    })
  ).toHaveTextContent('CN¥27.00')
  const summary = screen.getByRole('region', { name: 'Model profit summary' })
  expect(within(summary).getByText('CN¥27.00')).toBeVisible()
  expect(
    within(summary).getByText('Gross profit (known-cost portion)')
  ).toBeVisible()
  expect(within(summary).getByText('CN¥3.00')).toBeVisible()
  expect(within(summary).queryByText('CN¥22.00')).not.toBeInTheDocument()
})
