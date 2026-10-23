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
import type { AxiosAdapter } from 'axios'
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

import { ModelProfitsPage } from '..'
import type { ModelProfitResult, ModelProfitRow } from '../types'

const partial: ModelProfitRow = {
  model_name: 'partial-model',
  billing_events: 2,
  pending_billing_events: 0,
  customer_average_price_cny: 10,
  upstream_average_cost_cny: null,
  average_price_unit: 'request',
  cash_revenue_cny: null,
  gift_consumed_cny: null,
  cash_profit_cny: null,
  funding_status: 'unattributed',
  funding_unknown_events: 2,
  funding_coverage: 0,
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
  customer_average_price_cny: 9,
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
  cash_profit_basis: 'cash_attributed_revenue_minus_full_upstream_cost',
  funding_collection_status: 'not_collected',
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
const originalAdapter = api.defaults.adapter
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
  vi.useRealTimers()
  api.defaults.adapter = originalAdapter
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
    permissions: { admin_permissions: { model_profits: { read: true } } },
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
  renderReport()
  return get
}

function renderReport() {
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
}

function setProfitSession(id: number, sid: string, read = true) {
  const timestamp = Math.floor(Date.now() / 1000)
  useAuthStore.getState().auth.setBundle({
    access_token: `test-${sid}`,
    token_type: 'Bearer',
    access_expires_at: timestamp + 3600,
    user: {
      id,
      username: `admin-${id}`,
      role: ROLE.ADMIN,
      permissions: { admin_permissions: { model_profits: { read } } },
    },
    session: {
      sid,
      current: true,
      login_method: 'test',
      ip: '',
      user_agent: '',
      created_at: timestamp,
      last_active_at: timestamp,
      expires_at: timestamp + 3600,
    },
  })
}

it('keeps exactly five requested columns and leaves cash profit unconfirmed when allocations are missing', async () => {
  setup()
  const table = await screen.findByRole('table', { name: 'Profit by model' })
  expect(
    within(table)
      .getAllByRole('columnheader')
      .map((cell) => cell.textContent)
  ).toEqual([
    'Model name',
    'Usage count',
    'Customer unit price',
    'Procurement unit cost (estimate)',
    'Profit',
  ])
  const partialRow = within(table).getByRole('row', { name: /partial-model/ })
  expect(within(partialRow).getByText('CN¥10.00')).toBeVisible()
  expect(within(partialRow).getAllByRole('cell')[2]).toHaveTextContent(
    'Balance charges'
  )
  expect(within(partialRow).getAllByText('Pending confirmation')).toHaveLength(
    2
  )
  const summary = screen.getByRole('region', { name: 'Model profit summary' })
  expect(within(summary).getByText('Cash revenue')).toBeVisible()
  expect(within(summary).getByText('Cash profit')).toBeVisible()
  expect(within(summary).getAllByText('Pending confirmation')).toHaveLength(3)
  expect(within(summary).getByText('CN¥5.00')).toBeVisible()
  expect(within(summary).getByText('50%')).toBeVisible()
  expect(within(summary).queryByText('CN¥3.00')).not.toBeInTheDocument()
  expect(
    screen.getByText(/missing costs are never counted as zero/)
  ).toBeVisible()
  expect(
    screen.getByText(
      /New consumption also remains unverified until a funding ledger is implemented/
    )
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
      signal: expect.any(AbortSignal),
      disableDuplicate: true,
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
  expect(within(row).getByRole('cell', { name: /CN¥10\.00/ })).toHaveClass(
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
  expect(within(screen.getByRole('table')).getByText('¥10.00')).toBeVisible()
})

it('accepts an exact pasted model outside the current dropdown and clears it explicitly', async () => {
  const get = setup()
  await screen.findByText('partial-model')
  const user = userEvent.setup()
  const model = screen.getByRole('combobox', { name: 'Model' })
  await user.click(model)
  await user.paste('not-in-window-model')
  await user.tab()
  expect(model).toHaveValue('not-in-window-model')
  expect(get).toHaveBeenCalledOnce()
  await user.click(screen.getByRole('button', { name: 'Apply filters' }))
  await waitFor(() =>
    expect(get.mock.lastCall?.[1]).toMatchObject({
      params: { model_name: 'not-in-window-model' },
    })
  )
  await user.click(model)
  await user.clear(model)
  await user.click(screen.getByRole('option', { name: 'All models' }))
  await user.click(screen.getByRole('button', { name: 'Apply filters' }))
  await waitFor(() =>
    expect(get.mock.lastCall?.[1]).toMatchObject({
      params: { model_name: undefined },
    })
  )
})
it('accepts pasted calendar dates and exposes a separate date dropdown without submitting', async () => {
  const get = setup()
  await screen.findByText('partial-model')
  const user = userEvent.setup()
  const start = screen.getByLabelText('Start date')
  await user.click(start)
  await user.clear(start)
  await user.paste('2026-10-20')
  expect(start).toHaveValue('2026-10-20')
  await user.click(screen.getByRole('button', { name: 'Choose start date' }))
  expect(await screen.findByRole('grid')).toBeVisible()
  await user.click(
    screen.getByRole('button', { name: /Monday, October 19th, 2026/ })
  )
  expect(start).toHaveValue('2026-10-19')
  expect(get).toHaveBeenCalledOnce()
  await user.keyboard('{Escape}')
  fireEvent.change(start, { target: { value: '2026-02-30' } })
  expect(screen.getByRole('button', { name: 'Apply filters' })).toBeDisabled()
  expect(start).toHaveAttribute('aria-invalid', 'true')
})

it('switches to the dashboard using the same applied data and shows actual daily values', async () => {
  const get = setup()
  await screen.findByText('partial-model')
  fireEvent.click(screen.getByRole('tab', { name: 'Profit dashboard' }))
  const panel = screen.getByRole('tabpanel', { name: 'Profit dashboard' })
  expect(within(panel).getByText('Model balance charge ranking')).toBeVisible()
  expect(within(panel).getByText('Request distribution')).toBeVisible()
  expect(
    within(panel).getByText('Daily balance charges and requests')
  ).toBeVisible()
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
    screen.queryByRole('img', { name: 'Model balance charge ranking' })
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
  expect(within(summary).getAllByText('Pending confirmation')).toHaveLength(4)
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
    screen.queryByRole('img', { name: 'Model balance charge ranking' })
  ).not.toBeInTheDocument()
  get.mockResolvedValue({ data: { success: true, data: payload } })
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(
    await screen.findByRole('img', { name: 'Model balance charge ranking' })
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

it('labels pending reserved balance charges separately from unconfirmed cash profit', async () => {
  const pending = {
    ...partial,
    requests: 3,
    revenue_cny: 27,
    pending_requests: 1,
    pending_billing_events: 1,
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
      'Shanghai calendar days; balance charges and requests use separate axes. Charges include pending reserved amounts and gift balances; they are not cash revenue.'
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
  expect(within(summary).getByText('Cash profit')).toBeVisible()
  expect(within(summary).getAllByText('Pending confirmation')).toHaveLength(3)
  expect(within(summary).queryByText('CN¥22.00')).not.toBeInTheDocument()
})

it.each(['read permission', 'account', 'session'] as const)(
  'changing the %s aborts the earlier report and isolates a same-parameter fresh read',
  async (boundary) => {
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date('2026-10-24T02:00:00Z'))
    setProfitSession(1, 'first-session')
    const requests: {
      signal: { aborted?: boolean } | undefined
      complete: (data: ModelProfitResult) => void
    }[] = []
    const adapter: AxiosAdapter = (config) =>
      new Promise((resolve) => {
        requests.push({
          signal: config.signal,
          complete: (data) =>
            resolve({
              config,
              data: { success: true, data },
              status: 200,
              statusText: 'OK',
              headers: {},
            }),
        })
      })
    api.defaults.adapter = adapter
    try {
      renderReport()
      await waitFor(() => expect(requests).toHaveLength(1))
      if (boundary === 'read permission') {
        await act(async () => {
          setProfitSession(1, 'first-session', false)
        })
        expect(screen.getByText('Access denied')).toBeVisible()
        await act(async () => {
          setProfitSession(1, 'first-session')
        })
      } else if (boundary === 'account') {
        await act(async () => {
          setProfitSession(22, 'other-session')
        })
      } else {
        await act(async () => {
          setProfitSession(1, 'next-session')
        })
      }
      await waitFor(() => expect(requests).toHaveLength(2))
      expect(requests[0].signal?.aborted).toBe(true)
      await act(async () =>
        requests[0].complete({
          ...payload,
          rows: [{ ...partial, model_name: 'old-request-model' }],
        })
      )
      expect(screen.queryByText('old-request-model')).not.toBeInTheDocument()
      expect(screen.getByRole('status')).toHaveTextContent(
        'Loading model profits...'
      )
      await act(async () =>
        requests[1].complete({
          ...payload,
          rows: [{ ...partial, model_name: 'new-request-model' }],
        })
      )
      expect(await screen.findByText('new-request-model')).toBeVisible()
    } finally {
      await act(async () =>
        requests.forEach((request) =>
          request.complete({ ...payload, rows: [] })
        )
      )
    }
  }
)

it('older service responses keep absent cash and unit-price fields unconfirmed without deriving cash from balance charges', async () => {
  const legacy: ModelProfitRow = {
    ...partial,
    cash_revenue_cny: undefined,
    cash_profit_cny: undefined,
    gift_consumed_cny: undefined,
    funding_status: undefined,
    funding_coverage: undefined,
    funding_unknown_events: undefined,
    pending_billing_events: undefined,
    customer_average_price_cny: undefined,
    upstream_average_cost_cny: undefined,
    average_price_unit: undefined,
  }
  setup({
    data: {
      ...payload,
      funding_collection_status: undefined,
      rows: [legacy],
      summary: legacy,
    },
  })
  const row = await screen.findByRole('row', { name: /partial-model/ })
  expect(within(row).getAllByText('Pending confirmation')).toHaveLength(3)
  const summary = screen.getByRole('region', { name: 'Model profit summary' })
  expect(within(summary).getAllByText('Pending confirmation')).toHaveLength(3)
  expect(within(summary).getByText('Balance charges')).toBeVisible()
  expect(within(summary).getByText('CN¥20.00')).toBeVisible()
})

it('attributed synthetic records preserve real zero and negative cash profit while gift consumption retains its full cost', async () => {
  const gift: ModelProfitRow = {
    ...partial,
    model_name: 'verified-gift',
    known_cost_requests: 2,
    unknown_cost_requests: 0,
    known_cost_events: 2,
    unknown_cost_events: 0,
    cost_coverage: 1,
    upstream_cost_cny: 6,
    funding_status: 'attributed',
    funding_coverage: 1,
    funding_unknown_events: 0,
    cash_revenue_cny: 0,
    gift_consumed_cny: 20,
    cash_profit_cny: -6,
    upstream_average_cost_cny: 3,
  }
  const zero: ModelProfitRow = {
    ...gift,
    model_name: 'verified-zero',
    revenue_cny: 0,
    customer_average_price_cny: 0,
    upstream_average_cost_cny: 0,
    upstream_cost_cny: 0,
    cash_revenue_cny: 0,
    gift_consumed_cny: 0,
    cash_profit_cny: 0,
  }
  const loss: ModelProfitRow = {
    ...gift,
    model_name: 'verified-loss',
    cash_revenue_cny: 4,
    gift_consumed_cny: 16,
    cash_profit_cny: -2,
  }
  setup({
    data: {
      ...payload,
      funding_collection_status: 'collected',
      rows: [gift, zero, loss],
      summary: gift,
    },
  })
  const table = await screen.findByRole('table', { name: 'Profit by model' })
  expect(
    within(table).getByRole('row', { name: /verified-gift/ })
  ).toHaveTextContent('-CN¥6.00')
  expect(
    within(table).getByRole('row', { name: /verified-loss/ })
  ).toHaveTextContent('-CN¥2.00')
  expect(
    within(
      within(table).getByRole('row', { name: /verified-zero/ })
    ).getAllByText('CN¥0.00')
  ).toHaveLength(3)
  const summary = screen.getByRole('region', { name: 'Model profit summary' })
  expect(within(summary).getByText('CN¥0.00')).toBeVisible()
  expect(within(summary).getByText('CN¥6.00')).toBeVisible()
  expect(within(summary).getByText('-CN¥6.00')).toBeVisible()
  expect(
    within(summary).queryByText('Pending confirmation')
  ).not.toBeInTheDocument()
})

it('an unsettled zero-delta event blocks both period unit prices even when original calls and reserved balance are zero', async () => {
  const pending = {
    ...partial,
    requests: 0,
    pending_requests: 0,
    pending_revenue_cny: 0,
    pending_billing_events: 1,
    customer_average_price_cny: 0,
    upstream_average_cost_cny: 0,
  }
  setup({ data: { ...payload, rows: [pending], summary: pending } })
  const row = await screen.findByRole('row', { name: /partial-model/ })
  expect(within(row).getAllByText('Pending confirmation')).toHaveLength(3)
  expect(
    screen.getByText(
      /Billing events awaiting settlement: 1; original calls: 0;/
    )
  ).toBeVisible()
})

it.each([
  ['en', en],
  ['zhCN', zh],
  ['zhTW', zhTW],
  ['fr', fr],
  ['ru', ru],
  ['ja', ja],
  ['vi', viLocale],
] as const)(
  'switching to %s preserves the five-column semantics and the unconfirmed cash state',
  async (language, resource) => {
    i18n.addResourceBundle(
      language,
      'translation',
      resource.translation,
      true,
      true
    )
    setup()
    await screen.findByRole('table', { name: 'Profit by model' })
    await act(async () => {
      await i18n.changeLanguage(language)
    })
    const table = screen.getByRole('table', { name: i18n.t('Profit by model') })
    expect(
      within(table)
        .getAllByRole('columnheader')
        .map((cell) => cell.textContent)
    ).toEqual([
      i18n.t('Model name'),
      i18n.t('Usage count'),
      i18n.t('Customer unit price'),
      i18n.t('Procurement unit cost (estimate)'),
      i18n.t('Profit'),
    ])
    expect(
      within(table).getAllByText(i18n.t('Pending confirmation'))
    ).toHaveLength(4)
    expect(
      within(table).getAllByText(i18n.t('Average per request'))
    ).toHaveLength(4)
  }
)
