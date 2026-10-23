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
import { act, cleanup, render, screen, within } from '@testing-library/react'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, expect, it } from 'vitest'

import en from '@/i18n/locales/en.json'
import fr from '@/i18n/locales/fr.json'
import ja from '@/i18n/locales/ja.json'
import ru from '@/i18n/locales/ru.json'
import vi from '@/i18n/locales/vi.json'
import zhTW from '@/i18n/locales/zh-TW.json'
import zh from '@/i18n/locales/zh.json'

import { ProfitFormulas } from '../components/profit-formulas'
import { ProfitFundingDetails } from '../components/profit-funding-details'
import { ProfitFundingNotice } from '../components/profit-funding-notice'
import { ProfitTable } from '../components/profit-table'
import type { ModelProfitResult, ModelProfitRow } from '../types'

const resources = { en, fr, ja, ru, vi, zhCN: zh, zhTW }
const row: ModelProfitRow = {
  model_name: 'discount-and-gift-model',
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
  pending_billing_events: 0,
  pending_revenue_cny: 0,
  refund_cny: 0,
  cash_revenue_cny: null,
  gift_consumed_cny: null,
  cash_profit_cny: null,
  funding_status: 'unattributed',
  funding_coverage: 0.5,
  funding_unknown_events: 1,
  funding_policy: 'fifo',
  confirmed_cash_revenue_cny: 8,
  confirmed_paid_consumed_cny: 10,
  confirmed_gift_consumed_cny: 3,
  unknown_funding_consumed_cny: 7,
  customer_average_price_cny: 10,
  upstream_average_cost_cny: 3,
  average_price_unit: 'request',
}
const report: ModelProfitResult = {
  rows: [row],
  summary: row,
  timezone: 'Asia/Shanghai',
  currency: 'CNY',
  start_timestamp: 0,
  end_timestamp: 1,
  funding_collection_status: 'collected',
}
afterEach(cleanup)

async function languageInstance(language = 'en') {
  const i18n = createInstance()
  await i18n.init({
    lng: language,
    fallbackLng: 'en',
    keySeparator: false,
    nsSeparator: false,
    resources,
  })
  return i18n
}

it('keeps the five requested columns while exposing confirmed paid income, recharge face value, gifts and unknown consumption per model', async () => {
  const i18n = await languageInstance()
  render(
    <I18nextProvider i18n={i18n}>
      <ProfitFundingDetails data={row} />
      <ProfitTable rows={[row]} />
    </I18nextProvider>
  )
  const details = screen.getByRole('region', {
    name: 'Funding allocation details',
  })
  expect(within(details).getByText('CN¥8.00')).toBeVisible()
  expect(within(details).getByText('CN¥10.00')).toBeVisible()
  expect(within(details).getByText('CN¥3.00')).toBeVisible()
  expect(within(details).getByText('CN¥7.00')).toBeVisible()
  const table = screen.getByRole('table', { name: 'Profit by model' })
  expect(within(table).getAllByRole('columnheader')).toHaveLength(5)
  const cells = within(within(table).getAllByRole('row')[1]).getAllByRole(
    'cell'
  )
  expect(cells[0]).toHaveTextContent(
    'Confirmed paid consumption revenue: CN¥8.00'
  )
  expect(cells[0]).toHaveTextContent('Consumed paid face value: CN¥10.00')
  expect(cells[0]).toHaveTextContent('Consumed gifted face value: CN¥3.00')
  expect(cells[0]).toHaveTextContent(
    'Consumed face value awaiting attribution: CN¥7.00'
  )
  expect(cells[4]).toHaveTextContent('Pending confirmation')
})

it('shows missing ledger metadata as unconfirmed while preserving an explicitly recorded zero and negative refund', async () => {
  const i18n = await languageInstance()
  const view = render(
    <I18nextProvider i18n={i18n}>
      <ProfitFundingDetails data={{ ...row, funding_policy: undefined }} />
    </I18nextProvider>
  )
  expect(screen.getAllByText('Pending confirmation')).toHaveLength(4)
  view.rerender(
    <I18nextProvider i18n={i18n}>
      <ProfitFundingDetails
        data={{
          ...row,
          confirmed_cash_revenue_cny: 0,
          confirmed_paid_consumed_cny: -10,
        }}
      />
    </I18nextProvider>
  )
  expect(screen.getByText('CN¥0.00')).toBeVisible()
  expect(screen.getByText('-CN¥10.00')).toBeVisible()
})

it('shows the new collection policy only with explicit FIFO metadata and falls back safely for an old service', async () => {
  const i18n = await languageInstance()
  const view = render(
    <I18nextProvider i18n={i18n}>
      <ProfitFundingNotice report={report} />
    </I18nextProvider>
  )
  expect(screen.getByRole('alert')).toHaveTextContent(
    'Funding allocations are recorded for new operations.'
  )
  view.rerender(
    <I18nextProvider i18n={i18n}>
      <ProfitFundingNotice
        report={{ ...report, funding_collection_status: undefined }}
      />
    </I18nextProvider>
  )
  expect(screen.getByRole('alert')).toHaveTextContent(
    'Funding allocation metadata is unavailable'
  )
})

it.each(Object.keys(resources))(
  'shows real %s formula translations with original cash ratio and discount example',
  async (language) => {
    const i18n = await languageInstance(language)
    render(
      <I18nextProvider i18n={i18n}>
        <ProfitFormulas />
      </I18nextProvider>
    )
    const region = screen.getByRole('region', {
      name: i18n.t('Profit formulas'),
    })
    expect(within(region).getAllByRole('listitem')).toHaveLength(4)
    expect(region).toHaveTextContent(
      i18n.t(
        'Paid revenue = Σ(consumed paid face value × original cash received ÷ original credited face value).'
      )
    )
    expect(region).toHaveTextContent(
      i18n.t(
        'Actual model profit (estimate) = paid consumption revenue − total model procurement cost (estimate).'
      )
    )
    expect(region).not.toHaveTextContent('{{paid}}')
    expect(region).not.toHaveTextContent('{{revenue}}')
    expect(region).toHaveTextContent('80')
    expect(region).toHaveTextContent('100')
    if (language !== 'en') {
      expect(i18n.t('Profit formulas')).not.toBe('Profit formulas')
    }
  }
)

it('updates funding labels and currency formatting when the language changes', async () => {
  const i18n = await languageInstance()
  render(
    <I18nextProvider i18n={i18n}>
      <ProfitFundingDetails data={row} />
    </I18nextProvider>
  )
  expect(screen.getByText('CN¥8.00')).toBeVisible()
  await act(() => i18n.changeLanguage('zhCN'))
  expect(screen.getByRole('region', { name: '消费资金归属明细' })).toBeVisible()
  expect(screen.getByText('已确认实付消费收入')).toBeVisible()
  expect(screen.getByText('¥8.00')).toBeVisible()
})

it('does not classify proven paid face value as unknown-source consumption when only the cash receipt is missing', async () => {
  const i18n = await languageInstance()
  const receiptUnknown: ModelProfitRow = {
    ...row,
    revenue_cny: 10,
    confirmed_cash_revenue_cny: 0,
    confirmed_paid_consumed_cny: 10,
    confirmed_gift_consumed_cny: 0,
    unknown_funding_consumed_cny: 0,
    funding_unknown_events: 1,
    funding_coverage: 0,
    cash_profit_cny: null,
  }
  render(
    <I18nextProvider i18n={i18n}>
      <ProfitFundingDetails data={receiptUnknown} />
      <ProfitTable rows={[receiptUnknown]} />
    </I18nextProvider>
  )
  const details = screen.getByRole('region', {
    name: 'Funding allocation details',
  })
  expect(within(details).getByText('CN¥10.00')).toBeVisible()
  expect(within(details).getAllByText('CN¥0.00')).toHaveLength(3)
  expect(details).toHaveTextContent(
    'Missing cash receipt amounts or CNY evidence keep total profit pending confirmation.'
  )
  const table = screen.getByRole('table', { name: 'Profit by model' })
  const cells = within(within(table).getAllByRole('row')[1]).getAllByRole(
    'cell'
  )
  expect(cells[0]).toHaveTextContent(
    'Consumed face value awaiting attribution: CN¥0.00'
  )
  expect(cells[4]).toHaveTextContent('Pending confirmation')
})

it('keeps computable procurement cost and book margin visible while explaining separate historical funding gaps', async () => {
  const i18n = await languageInstance()
  render(
    <I18nextProvider i18n={i18n}>
      <ProfitFundingDetails
        data={{
          ...row,
          unknown_cost_reasons: {
            image_resolution_missing: 2,
            procurement_price_missing: 1,
          },
          unconfirmed_funding_reasons: {
            historical_funding_evidence_missing: 3,
          },
        }}
      />
    </I18nextProvider>
  )
  expect(screen.getByText('CN¥14.00')).toBeVisible()
  expect(
    screen.getByText('Book margin on cost-covered consumption', {
      exact: false,
    })
  ).toBeVisible()
  const costs = screen.getByRole('region', { name: 'Missing cost evidence' })
  expect(
    within(costs).getByText('Original billed image resolution is missing:', {
      exact: false,
    })
  ).toHaveTextContent('Original billed image resolution is missing: 2')
  expect(
    within(costs).getByText(
      'Procurement price missing for this channel, model or effective period:',
      { exact: false }
    )
  ).toHaveTextContent(
    'Procurement price missing for this channel, model or effective period: 1'
  )
  const funding = screen.getByRole('region', {
    name: 'Missing paid-versus-gift evidence',
  })
  expect(
    within(funding).getByText(
      'Historical paid-versus-gift allocation evidence is missing:',
      { exact: false }
    )
  ).toHaveTextContent(
    'Historical paid-versus-gift allocation evidence is missing: 3'
  )
  expect(
    screen.queryByText('procurement_price_missing')
  ).not.toBeInTheDocument()
})
it('old service responses omit reason groups and future reason codes use a readable fallback without displaying invalid counts', async () => {
  const i18n = await languageInstance()
  const view = render(
    <I18nextProvider i18n={i18n}>
      <ProfitFundingDetails data={row} />
    </I18nextProvider>
  )
  expect(
    screen.queryByRole('region', { name: 'Missing cost evidence' })
  ).not.toBeInTheDocument()
  view.rerender(
    <I18nextProvider i18n={i18n}>
      <ProfitFundingDetails
        data={{
          ...row,
          unknown_cost_reasons: {
            future_evidence_code: 2,
            invalid_usage_facts: -1,
            pending_settlement: Number.NaN,
          },
        }}
      />
    </I18nextProvider>
  )
  expect(
    screen.getByText('Other missing accounting evidence:', { exact: false })
  ).toHaveTextContent('Other missing accounting evidence: 2')
  expect(screen.queryByText('NaN')).not.toBeInTheDocument()
  expect(
    screen.queryByText('Recorded billable usage is invalid')
  ).not.toBeInTheDocument()
})
