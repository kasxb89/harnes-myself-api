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
import { Route } from '@/routes/_authenticated/balance-ledger'
import { useAuthStore } from '@/stores/auth-store'

import { BalanceLedger } from '..'
import type { BalanceEntry, BalanceFilters, BalanceSummary } from '../types'

const registration: BalanceEntry = {
  id: 'log:1',
  created_at: 1792398000,
  user_id: 42,
  username: 'alice',
  category: 'registration_bonus',
  action: 'user.register',
  quota_delta: null,
  quota_before: null,
  quota_after: null,
  paid_money: null,
  paid_currency: null,
  payment_provider: '',
  operator_id: null,
  operator_username: null,
  source: 'log',
  amount_known: false,
  display_amount: '¥10.00',
  content: 'New user registration bonus',
  legacy: true,
}
const adjustment: BalanceEntry = {
  ...registration,
  id: 'log:2',
  user_id: 43,
  username: 'bob',
  category: 'admin_adjustment',
  action: 'user.quota_override',
  quota_before: 5000000,
  quota_after: 6000000,
  quota_delta: 1000000,
  amount_known: true,
  display_amount: null,
  operator_id: 1,
  operator_username: 'root-operator',
  content: 'Override balance to $12',
  legacy: false,
}
const payment: BalanceEntry = {
  ...registration,
  id: 'top_up:1',
  created_at: 0,
  category: 'online_topup',
  action: 'topup.completed',
  quota_delta: 5000000,
  amount_known: true,
  paid_money: 70,
  paid_currency: 'CNY',
  payment_provider: 'wechat',
  display_amount: null,
  source: 'top_up',
  content: '',
  legacy: false,
}
const summary: BalanceSummary = {
  paid_money: 350,
  paid_currency: 'CNY',
  paid_orders: 5,
  paid_totals: [
    { currency: 'CNY', payment_provider: 'wechat', amount: 350, orders: 5 },
  ],
  registration_bonus_quota: 0,
  registration_bonus_count: 5,
  registration_bonus_unknown: 5,
  registration_coverage: { complete: true, reason: '', relevant: true },
  admin_increase_quota: 1000000,
  admin_decrease_quota: 500000,
  admin_adjustments: 3,
  unknown_count: 5,
  other_unknown: 0,
  raw_quota_only: true,
  categories: [],
}
const testI18n = createInstance()
const clients: QueryClient[] = []
beforeEach(async () => {
  await testI18n.init({
    lng: 'en',
    fallbackLng: 'en',
    keySeparator: false,
    nsSeparator: false,
    resources: { en, zh },
  })
})
afterEach(() => {
  cleanup()
  clients.splice(0).forEach((client) => client.clear())
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})
function setup(
  options: {
    empty?: boolean
    fail?: boolean
    role?: number
    mobile?: boolean
    unknownCurrency?: boolean
    largeQuota?: boolean
    summaryOverride?: Partial<BalanceSummary>
    omitRegistrationEntries?: boolean
  } = {}
) {
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'admin',
    role: options.role ?? ROLE.ADMIN,
    permissions: { admin_permissions: { balance_ledger: { read: true } } },
  })
  if (options.mobile) {
    const matchMedia = window.matchMedia
    vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
      ...matchMedia(query),
      matches: query === '(max-width: 640px)',
    }))
  }
  const get = vi.spyOn(api, 'get').mockImplementation(async (_url, config) => {
    if (options.fail) {
      return { data: { success: false, message: 'Ledger unavailable' } }
    }
    const filters = config?.params as BalanceFilters
    let items = options.empty ? [] : [registration, adjustment, payment]
    if (options.omitRegistrationEntries) {
      items = items.filter((entry) => entry.category !== 'registration_bonus')
    }
    if (options.largeQuota) {
      items = [
        {
          ...adjustment,
          quota_delta: 9223372036854775808,
          quota_delta_exact: '9223372036854775808',
          quota_before: -9223372036854775808,
          quota_before_exact: '-9223372036854775808',
          quota_after: 0,
          quota_after_exact: '0',
        },
      ]
    }
    if (filters.category !== 'all') {
      items = items.filter((entry) => entry.category === filters.category)
    }
    if (filters.user_id) {
      items = items.filter((entry) => entry.user_id === filters.user_id)
    }
    if (filters.p === 2) {
      items = [{ ...registration, id: 'log:99', username: 'page-two-user' }]
    }
    const currencySummary = options.unknownCurrency
      ? {
          ...summary,
          paid_money: null,
          paid_currency: null,
          paid_totals: [
            {
              currency: null,
              payment_provider: 'stripe',
              amount: 9,
              orders: 1,
            },
          ],
        }
      : summary
    const resultSummary = { ...currencySummary, ...options.summaryOverride }
    return {
      data: {
        success: true,
        data: {
          items,
          total: options.empty ? 0 : 41,
          p: filters.p,
          page_size: filters.page_size,
          summary: resultSummary,
        },
      },
    }
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  render(
    <I18nextProvider i18n={testI18n}>
      <QueryClientProvider client={client}>
        <BalanceLedger />
      </QueryClientProvider>
    </I18nextProvider>
  )
  return get
}

it('shows whole-filter payment totals and preserves historical registration amounts without claiming a zero gift total', async () => {
  setup()
  await screen.findByText('bob')
  const totals = screen.getByRole('region', { name: 'Balance summary' })
  expect(within(totals).getByText('350 CNY')).toBeVisible()
  expect(within(totals).getByText('5 gift records')).toBeVisible()
  expect(
    within(totals).getByText(/5 historical records lack raw quota/)
  ).toBeVisible()
  expect(screen.getByText('Historical recorded amount: ¥10.00')).toBeVisible()
  expect(screen.getByText('70 CNY')).toBeVisible()
  expect(screen.queryByText(/1970-01-01/)).not.toBeInTheDocument()
})
it('does not claim zero registration gifts when the server has not confirmed complete historical coverage', async () => {
  setup({
    omitRegistrationEntries: true,
    summaryOverride: {
      registration_bonus_count: 0,
      registration_bonus_quota: 0,
      registration_bonus_unknown: 0,
      registration_coverage: undefined,
      unknown_count: 0,
    },
  })
  await screen.findByText('bob')
  const totals = screen.getByRole('region', { name: 'Balance summary' })
  expect(
    within(totals).getByText('Registration gift history unverified')
  ).toBeVisible()
  expect(
    within(totals).queryByText('Registration gift records: 0')
  ).not.toBeInTheDocument()
})
it('labels a historical registration gap in Chinese without inferring gifts from balances or current settings', async () => {
  setup({
    omitRegistrationEntries: true,
    summaryOverride: {
      registration_bonus_count: 0,
      registration_bonus_quota: 0,
      registration_bonus_unknown: 0,
      unknown_count: 0,
      registration_coverage: {
        complete: false,
        reason: 'historical_unrecorded',
        relevant: true,
      },
    },
  })
  await screen.findByText('bob')
  await act(async () => testI18n.changeLanguage('zh'))
  const totals = screen.getByRole('region', { name: '额度汇总' })
  expect(within(totals).getByText('历史赠送未记录')).toBeVisible()
  expect(
    within(totals).getByText(
      '已有用户的历史赠送尚无原始流水，本页不据余额或当前设置反推。'
    )
  ).toBeVisible()
  expect(within(totals).queryByText('$0')).not.toBeInTheDocument()
  expect(screen.queryByText('历史记录金额: ¥10.00')).not.toBeInTheDocument()
})
it('counts only observed gift records when historical coverage is incomplete', async () => {
  setup({
    summaryOverride: {
      registration_coverage: {
        complete: false,
        reason: 'historical_unrecorded',
        relevant: true,
      },
    },
  })
  await screen.findByText('bob')
  const totals = screen.getByRole('region', { name: 'Balance summary' })
  expect(
    within(totals).getByText('Recorded registration gifts: 5')
  ).toBeVisible()
  expect(
    within(totals).getByText('Only recorded registration gifts are counted')
  ).toBeVisible()
})
it('shows a zero gift amount only when the server confirms complete coverage', async () => {
  setup({
    omitRegistrationEntries: true,
    summaryOverride: {
      registration_bonus_count: 0,
      registration_bonus_quota: 0,
      registration_bonus_unknown: 0,
      unknown_count: 0,
    },
  })
  await screen.findByText('bob')
  const totals = screen.getByRole('region', { name: 'Balance summary' })
  expect(within(totals).getByText('$0')).toBeVisible()
  expect(within(totals).getByText('Registration gift records: 0')).toBeVisible()
  expect(
    within(totals).queryByText(/lack original ledger records/)
  ).not.toBeInTheDocument()
})
it('marks registration gifts outside the chosen source instead of showing a zero gift total', async () => {
  setup({
    omitRegistrationEntries: true,
    summaryOverride: {
      registration_bonus_count: 0,
      registration_bonus_quota: 0,
      registration_bonus_unknown: 0,
      unknown_count: 0,
      registration_coverage: {
        complete: false,
        reason: 'historical_unrecorded',
        relevant: false,
      },
    },
  })
  await screen.findByText('bob')
  const totals = screen.getByRole('region', { name: 'Balance summary' })
  expect(
    within(totals).getByText('Outside the selected balance source')
  ).toBeVisible()
  expect(within(totals).queryByText('$0')).not.toBeInTheDocument()
  expect(
    within(totals).queryByText(/lack original ledger records/)
  ).not.toBeInTheDocument()
})
it('shows an administrator override as a signed difference with the operator and before/after amounts', async () => {
  setup()
  await screen.findByText('bob')
  const row = screen.getByRole('row', { name: /bob/ })
  expect(within(row).getByText('Override user quota')).toBeVisible()
  expect(within(row).getByText('+$2')).toBeVisible()
  expect(within(row).getByText('Before and after: $10 → $12')).toBeVisible()
  expect(within(row).getByText('root-operator')).toBeVisible()
})
it('shows exact raw quota for an extreme override instead of rounding its integer values', async () => {
  setup({ largeQuota: true })
  await screen.findByText('bob')
  expect(screen.getByText('Raw quota: 9223372036854775808')).toBeVisible()
  expect(
    screen.getByText('Before and after: Raw quota: -9223372036854775808 → $0')
  ).toBeVisible()
})
it('filters a selected source and user while resetting pagination and sending only committed user input', async () => {
  const get = setup()
  await screen.findByText('bob')
  await userEvent.click(screen.getByRole('button', { name: 'Go to next page' }))
  await screen.findByText('page-two-user')
  await userEvent.click(
    screen.getByRole('combobox', { name: 'Balance source' })
  )
  await userEvent.click(
    await screen.findByRole('option', {
      name: /^Registration gift$/,
    })
  )
  await waitFor(() =>
    expect(get.mock.lastCall?.[1]?.params).toMatchObject({
      category: 'registration_bonus',
      p: 1,
    })
  )
  const requestCount = get.mock.calls.length
  await userEvent.type(screen.getByRole('textbox', { name: 'User ID' }), '42')
  expect(get).toHaveBeenCalledTimes(requestCount)
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(get.mock.lastCall?.[1]?.params).toMatchObject({
      category: 'registration_bonus',
      user_id: 42,
      p: 1,
    })
  )
  expect(screen.queryByText('bob')).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: 'Reset' }))
  await screen.findByText('bob')
  expect(get.mock.lastCall?.[1]?.params).toEqual({
    category: 'all',
    p: 1,
    page_size: 20,
  })
})
it('rejects invalid user IDs without requesting or presenting the preceding records', async () => {
  const get = setup()
  await screen.findByText('bob')
  const count = get.mock.calls.length
  await userEvent.type(screen.getByRole('textbox', { name: 'User ID' }), '-1')
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  expect(
    await screen.findByText('Enter a positive whole-number user ID')
  ).toBeVisible()
  expect(screen.getByRole('textbox', { name: 'User ID' })).toHaveAttribute(
    'aria-invalid',
    'true'
  )
  expect(screen.queryByText('bob')).not.toBeInTheDocument()
  expect(get).toHaveBeenCalledTimes(count)
})
it('exposes empty and recoverable server failure states', async () => {
  const options = { fail: true, empty: true }
  setup(options)
  await screen.findByRole('button', { name: 'Retry' })
  options.fail = false
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(await screen.findByText('No balance records')).toBeVisible()
  expect(
    screen.getByText('Try another balance source, user or period.')
  ).toBeVisible()
})
it('keeps unknown payment currency separate from quota currency and identifies its provider', async () => {
  setup({ unknownCurrency: true })
  const totals = await screen.findByRole('region', { name: 'Balance summary' })
  await within(totals).findByText(/stripe: 9/)
  expect(
    within(totals).getByText(/must not be added across currencies/)
  ).toBeVisible()
  expect(within(totals).queryByText('$9')).not.toBeInTheDocument()
})
it('keeps the financial source and administrator details readable on mobile', async () => {
  setup({ mobile: true })
  await screen.findByText('bob')
  expect(screen.queryByRole('table')).not.toBeInTheDocument()
  expect(screen.getByText('root-operator')).toBeVisible()
  expect(screen.getByText('Before and after: $10 → $12')).toBeVisible()
  expect(screen.getByRole('combobox', { name: 'Balance source' })).toBeVisible()
})
it('does not request ledger data for ordinary users and redirects them at the route boundary', async () => {
  const get = setup({ role: ROLE.USER })
  expect(await screen.findByText('Access denied')).toBeVisible()
  expect(get).not.toHaveBeenCalled()
  await expect(async () =>
    Route.options.beforeLoad?.({} as never)
  ).rejects.toMatchObject({ options: { to: '/403' } })
})
it('does not request an invalid date range and hides the preceding data', async () => {
  const get = setup()
  await screen.findByText('bob')
  await userEvent.click(screen.getByRole('button', { name: 'Date Range' }))
  fireEvent.change(screen.getByLabelText('Start Time'), {
    target: { value: '2026-10-20T12:00:00' },
  })
  fireEvent.change(screen.getByLabelText('End Time'), {
    target: { value: '2026-10-19T12:00:00' },
  })
  const count = get.mock.calls.length
  await userEvent.click(screen.getByRole('button', { name: 'Confirm' }))
  expect(
    await screen.findByText('End time must be after start time')
  ).toBeVisible()
  expect(screen.queryByText('bob')).not.toBeInTheDocument()
  expect(get).toHaveBeenCalledTimes(count)
})

it('updates source labels and historical amount explanations when the interface language changes', async () => {
  setup()
  await screen.findByText('bob')
  await act(async () => testI18n.changeLanguage('zh'))
  expect(screen.getByRole('combobox', { name: '额度来源' })).toBeVisible()
  expect(screen.getByText('5 笔赠送记录')).toBeVisible()
  expect(screen.getByText('历史记录金额: ¥10.00')).toBeVisible()
  const adjustmentRow = screen.getByRole('row', { name: /bob/ })
  expect(
    within(adjustmentRow).getByText('调整方式: 覆盖用户额度')
  ).toBeVisible()
  expect(
    screen.queryByText('New user registration bonus')
  ).not.toBeInTheDocument()
  expect(screen.queryByText('Override balance to $12')).not.toBeInTheDocument()
})
