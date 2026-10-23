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
import { expect, it } from 'vitest'

import { toIntlLocale } from '@/i18n/languages'

import {
  formatProfitCny,
  formatProfitMargin,
  verifiedCashAmount,
  verifiedFundingAmount,
  verifiedPeriodAverage,
} from '../lib/format'
import type { ModelProfitRow } from '../types'

it.each(['en', 'zhCN', 'zhTW', 'fr', 'ru', 'ja', 'vi', 'invalid_tag_!'])(
  'formats CNY independently of the display currency with safe language %s',
  (language) => {
    const locale = toIntlLocale(language)
    expect(formatProfitCny(1234.56789, locale)).toContain('56789')
    expect(formatProfitCny(null, locale)).toBe('—')
    expect(formatProfitCny(0.000000001, locale)).toMatch(/^</)
    expect(formatProfitCny(-0.000000001, locale)).toMatch(/^>/)
    expect(formatProfitMargin(null, locale)).toBe('—')
    expect(formatProfitMargin(-0.00001993, locale)).toMatch(/[-−]/)
    expect(formatProfitMargin(-0.00001993, locale)).not.toMatch(/^[-−]0[%\s]/)
    expect(formatProfitMargin(0.000000001, locale)).toMatch(/^</)
    expect(formatProfitMargin(-0.000000001, locale)).toMatch(/^>/)
  }
)

const attributed: ModelProfitRow = {
  model_name: 'verified-gift-model',
  requests: 2,
  billing_events: 2,
  revenue_cny: 20,
  known_cost_revenue_cny: 20,
  upstream_cost_cny: 6,
  gross_profit_cny: 14,
  profit_margin: 0.7,
  known_cost_requests: 2,
  unknown_cost_requests: 0,
  known_cost_events: 2,
  unknown_cost_events: 0,
  cost_coverage: 1,
  pending_requests: 0,
  pending_revenue_cny: 0,
  pending_billing_events: 0,
  refund_cny: 0,
  cash_revenue_cny: 0,
  gift_consumed_cny: 20,
  cash_profit_cny: -6,
  funding_status: 'attributed',
  funding_coverage: 1,
  funding_unknown_events: 0,
  customer_average_price_cny: 10,
  upstream_average_cost_cny: 3,
  average_price_unit: 'request',
}

it('verified gift consumption retains zero cash revenue and the full negative upstream profit', () => {
  expect(verifiedCashAmount(attributed, 'cash_revenue_cny')).toBe(0)
  expect(verifiedCashAmount(attributed, 'gift_consumed_cny')).toBe(20)
  expect(verifiedCashAmount(attributed, 'cash_profit_cny')).toBe(-6)
})

it.each([
  {
    condition: 'an older service omits attribution status',
    change: { funding_status: undefined },
  },
  {
    condition: 'funding is explicitly unattributed',
    change: { funding_status: 'unattributed' },
  },
  {
    condition: 'only some funding is covered',
    change: { funding_coverage: 0.5 },
  },
  {
    condition: 'a funding event is unknown',
    change: { funding_unknown_events: 1 },
  },
  { condition: 'an original call is pending', change: { pending_requests: 1 } },
  {
    condition: 'a zero-delta billing event is pending',
    change: { pending_billing_events: 1 },
  },
  {
    condition: 'the pending-event count is absent',
    change: { pending_billing_events: undefined },
  },
  {
    condition: 'the returned cash amount is not finite',
    change: { cash_revenue_cny: Number.NaN },
  },
])('cash revenue stays unknown when $condition', ({ change }) => {
  expect(
    verifiedCashAmount({ ...attributed, ...change }, 'cash_revenue_cny')
  ).toBeNull()
})

it('cash profit stays unknown when full upstream cost evidence is missing', () => {
  expect(
    verifiedCashAmount(
      { ...attributed, unknown_cost_events: 1 },
      'cash_profit_cny'
    )
  ).toBeNull()
  expect(
    verifiedCashAmount(
      { ...attributed, upstream_cost_cny: null },
      'cash_profit_cny'
    )
  ).toBeNull()
  expect(
    verifiedCashAmount(
      { ...attributed, cash_profit_cny: 0, upstream_cost_cny: 0 },
      'cash_profit_cny'
    )
  ).toBe(0)
})

it('period unit prices trust explicit per-request values while allowing a real zero', () => {
  expect(verifiedPeriodAverage(attributed, 'customer_average_price_cny')).toBe(
    10
  )
  expect(verifiedPeriodAverage(attributed, 'upstream_average_cost_cny')).toBe(3)
  expect(
    verifiedPeriodAverage(
      { ...attributed, customer_average_price_cny: 0 },
      'customer_average_price_cny'
    )
  ).toBe(0)
})

it.each([
  {
    condition: 'a refund-only period has no original calls',
    change: { requests: 0 },
  },
  {
    condition: 'an original call awaits settlement',
    change: { pending_requests: 1 },
  },
  {
    condition: 'a zero-delta billing event awaits settlement',
    change: { pending_billing_events: 1 },
  },
  {
    condition: 'pending-event coverage is absent',
    change: { pending_billing_events: undefined },
  },
  {
    condition: 'the response does not declare per-request units',
    change: { average_price_unit: undefined },
  },
  {
    condition: 'an older service omits the average',
    change: { customer_average_price_cny: undefined },
  },
  {
    condition: 'the returned average is not finite',
    change: { customer_average_price_cny: Number.POSITIVE_INFINITY },
  },
])('customer unit price remains unknown when $condition', ({ change }) => {
  expect(
    verifiedPeriodAverage(
      { ...attributed, ...change },
      'customer_average_price_cny'
    )
  ).toBeNull()
})

it('partial upstream cost coverage cannot be presented as a whole-row average cost', () => {
  expect(
    verifiedPeriodAverage(
      { ...attributed, unknown_cost_events: 1, cost_coverage: 0.5 },
      'upstream_average_cost_cny'
    )
  ).toBeNull()
})

const mixedFunding: ModelProfitRow = {
  ...attributed,
  funding_policy: 'fifo',
  funding_status: 'unattributed',
  funding_coverage: 0.5,
  funding_unknown_events: 1,
  cash_revenue_cny: null,
  gift_consumed_cny: null,
  cash_profit_cny: null,
  confirmed_cash_revenue_cny: 8,
  confirmed_paid_consumed_cny: 10,
  confirmed_gift_consumed_cny: 3,
  unknown_funding_consumed_cny: 7,
}

it('shows confirmed funding parts without substituting recharge face value for actual paid revenue or asserting total profit', () => {
  expect(
    verifiedFundingAmount(mixedFunding, 'confirmed_cash_revenue_cny')
  ).toBe(8)
  expect(
    verifiedFundingAmount(mixedFunding, 'confirmed_paid_consumed_cny')
  ).toBe(10)
  expect(
    verifiedFundingAmount(mixedFunding, 'confirmed_gift_consumed_cny')
  ).toBe(3)
  expect(
    verifiedFundingAmount(mixedFunding, 'unknown_funding_consumed_cny')
  ).toBe(7)
  expect(verifiedCashAmount(mixedFunding, 'cash_profit_cny')).toBeNull()
  expect(verifiedCashAmount(mixedFunding, 'cash_revenue_cny')).toBeNull()
})

it('retains a real zero and refund-only negative funding parts rather than clamping them', () => {
  expect(
    verifiedFundingAmount(
      { ...mixedFunding, confirmed_cash_revenue_cny: 0 },
      'confirmed_cash_revenue_cny'
    )
  ).toBe(0)
  expect(
    verifiedFundingAmount(
      { ...mixedFunding, confirmed_cash_revenue_cny: -8 },
      'confirmed_cash_revenue_cny'
    )
  ).toBe(-8)
})

it.each([
  { funding_policy: undefined },
  { confirmed_cash_revenue_cny: undefined },
  { confirmed_cash_revenue_cny: null },
  { confirmed_cash_revenue_cny: Number.NaN },
  { confirmed_cash_revenue_cny: Number.POSITIVE_INFINITY },
])(
  'does not invent confirmed funding from absent or malformed ledger metadata %o',
  (change) => {
    expect(
      verifiedFundingAmount(
        { ...mixedFunding, ...change },
        'confirmed_cash_revenue_cny'
      )
    ).toBeNull()
  }
)
