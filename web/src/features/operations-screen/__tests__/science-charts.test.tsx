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
import { fireEvent, render, screen, within } from '@testing-library/react'
import { beforeEach, expect, it, vi } from 'vitest'

import { emptyMediaPayload } from '@/features/media-operations/__tests__/fixtures'
import type { ModelProfitResult } from '@/features/model-profits/types'
import { usageLogSchema } from '@/features/usage-logs/data/schema'

import type { OperationsSnapshot } from '../api'
import { OperationsScienceCharts } from '../components/science-charts'
import { OperationsSnapshotContent } from '../components/snapshot-content'

beforeEach(() => {
  vi.spyOn(HTMLElement.prototype, 'getBoundingClientRect').mockImplementation(
    function (this: HTMLElement) {
      return new DOMRect(
        0,
        0,
        560,
        this.closest('.recharts-legend-wrapper') ? 24 : 320
      )
    }
  )
})

const window = { start_timestamp: 100, end_timestamp: 200 }
const row = usageLogSchema.parse({
  id: 1,
  user_id: 7,
  created_at: 150,
  type: 2,
  content: '',
  model_name: 'fixture-model',
  prompt_tokens: 10,
  completion_tokens: 4,
})
const usage: Extract<OperationsSnapshot, { tab: 'usage' }> = {
  tab: 'usage',
  window,
  logs: { items: [row, { ...row, id: 2 }], total: 123 },
  stats: { rpm: 400, tpm: 500, quota: 100000 },
}
const totals = {
  requests: 2,
  billing_events: 2,
  revenue_cny: -2,
  known_cost_revenue_cny: -2,
  upstream_cost_cny: -1,
  gross_profit_cny: -1,
  profit_margin: 0.5,
  known_cost_requests: 1,
  unknown_cost_requests: 1,
  known_cost_events: 1,
  unknown_cost_events: 1,
  pending_requests: 0,
  pending_billing_events: 0,
  pending_revenue_cny: 0,
  refund_cny: 2,
  cost_coverage: 0.5,
}
const profit: ModelProfitResult = {
  summary: totals,
  rows: [
    { ...totals, model_name: 'refund-model' },
    {
      ...totals,
      revenue_cny: 0,
      upstream_cost_cny: null,
      model_name: 'unknown-cost',
    },
  ],
  timezone: 'Asia/Shanghai',
  currency: 'CNY',
  ...window,
  funding_collection_status: 'not_collected',
}

it('renders two real charts with page sample scope and preserves detailed records plus readable chart values', () => {
  render(<OperationsSnapshotContent snapshot={usage} />)
  expect(screen.getAllByRole('img')).toHaveLength(2)
  expect(
    screen.getAllByRole('img').every((chart) => chart.querySelector('svg'))
  ).toBe(true)
  expect(
    screen.getByRole('img', {
      name: 'Model consumption records (page sample) · Log records',
    })
  ).toBeVisible()
  expect(
    screen.getByRole('img', { name: 'Token directions (page sample) · Tokens' })
  ).toBeVisible()
  expect(
    screen.getAllByText(
      'Chart sample: 2 returned log records of 123 matches. Settlement records are not distinct API calls.'
    )
  ).toHaveLength(2)
  expect(
    screen.getByRole('table', { name: 'Recent consumption records' })
  ).toBeVisible()
  const modelPanel = screen.getByRole('region', {
    name: 'Model consumption records (page sample)',
  })
  fireEvent.click(within(modelPanel).getByText('Chart values'))
  const values = within(modelPanel).getByRole('table', {
    name: 'Chart values · Model consumption records (page sample)',
  })
  expect(values).toBeVisible()
  expect(within(values).getByText('fixture-model')).toBeVisible()
  expect(within(values).getByText('2')).toBeVisible()
  expect(within(values).queryByText('123')).not.toBeInTheDocument()
})
it('uses CNY values with negative refunds and exposes unknown cost instead of a zero cost or estimated cash profit', () => {
  render(
    <OperationsSnapshotContent
      snapshot={{ tab: 'profit', window, report: profit }}
    />
  )
  const panel = screen.getByRole('region', {
    name: 'Model balance charges and known costs',
  })
  expect(
    within(panel).getByRole('img', {
      name: 'Model balance charges and known costs · CNY (¥)',
    })
  ).toBeVisible()
  expect(
    within(panel).getByText('Unknown values are not plotted as zero.')
  ).toBeVisible()
  fireEvent.click(within(panel).getByText('Chart values'))
  const table = within(panel).getByRole('table')
  const refund = within(table).getByRole('row', { name: /refund-model/ })
  expect(within(refund).getByText(/-CN¥2\.00/)).toBeVisible()
  expect(within(refund).getByText(/-CN¥1\.00/)).toBeVisible()
  const unknown = within(table).getByRole('row', { name: /unknown-cost/ })
  expect(within(unknown).getByText('Unknown')).toBeVisible()
  expect(within(unknown).getByText(/CN¥0\.00/)).toBeVisible()
  expect(
    screen.getByText(
      'Paid-versus-gift funding allocations are not collected in this version. New consumption also remains unverified until a funding ledger is implemented.'
    )
  ).toBeVisible()
  expect(screen.getAllByText('Pending confirmation').length).toBeGreaterThan(0)
})
it('does not draw a fabricated ring for all-zero persisted tasks and labels task units correctly', () => {
  render(
    <OperationsScienceCharts
      snapshot={{ tab: 'video', window, report: emptyMediaPayload }}
    />
  )
  const panel = screen.getByRole('region', {
    name: 'Persisted video task states',
  })
  expect(within(panel).getByRole('status')).toHaveTextContent(
    'All observed counts are zero'
  )
  expect(within(panel).queryByRole('img')).not.toBeInTheDocument()
  fireEvent.click(within(panel).getByText('Chart values'))
  const table = within(panel).getByRole('table')
  expect(
    within(table).getByRole('columnheader', { name: 'Count · Task records' })
  ).toBeVisible()
  expect(within(table).getAllByText('0')).toHaveLength(5)
  expect(
    within(
      screen.getByRole('region', { name: 'Video error stages' })
    ).getByRole('status')
  ).toHaveTextContent('No chart observations')
})
it('keeps missing old cost-evidence fields unavailable even with zero pending requests', () => {
  render(
    <OperationsScienceCharts
      snapshot={{
        tab: 'profit',
        window,
        report: {
          ...profit,
          summary: { ...totals, pending_billing_events: undefined },
        },
      }}
    />
  )
  const panel = screen.getByRole('region', { name: 'Billing cost evidence' })
  expect(within(panel).getByRole('status')).toHaveTextContent(
    'Cost evidence is unavailable for this response.'
  )
  expect(within(panel).queryByRole('img')).not.toBeInTheDocument()
})

it('keeps both models in each bar series the same color as its legend without sharing category colors across series', () => {
  render(
    <OperationsScienceCharts
      snapshot={{
        tab: 'profit',
        window,
        report: {
          ...profit,
          rows: [
            {
              ...totals,
              model_name: 'demo-image',
              revenue_cny: 5,
              upstream_cost_cny: 1,
            },
            {
              ...totals,
              model_name: 'demo-chat',
              revenue_cny: 3,
              upstream_cost_cny: 2,
            },
          ],
        },
      }}
    />
  )
  const chart = screen.getByRole('img', {
    name: 'Model balance charges and known costs · CNY (¥)',
  })
  const series = chart.querySelectorAll('.recharts-bar')
  expect(series).toHaveLength(2)
  const chargeFills = [
    ...series[0].querySelectorAll('path.recharts-rectangle'),
  ].map((bar) => bar.getAttribute('fill'))
  const costFills = [
    ...series[1].querySelectorAll('path.recharts-rectangle'),
  ].map((bar) => bar.getAttribute('fill'))
  expect(chargeFills).toEqual(['var(--color-value)', 'var(--color-value)'])
  expect(costFills).toEqual([
    'var(--color-secondary)',
    'var(--color-secondary)',
  ])
  expect(
    within(chart).getByText('Balance charges').firstElementChild
  ).toHaveStyle({ backgroundColor: 'var(--color-value)' })
  expect(
    within(chart).getByText('Known model procurement cost (estimate)')
      .firstElementChild
  ).toHaveStyle({ backgroundColor: 'var(--color-secondary)' })
})

it('can mount one real chart per HUD flank without duplicating charts inside retained details', () => {
  render(
    <>
      <OperationsScienceCharts snapshot={usage} slot='left' />
      <OperationsScienceCharts snapshot={usage} slot='right' />
      <OperationsSnapshotContent snapshot={usage} showCharts={false} />
    </>
  )
  expect(screen.getAllByRole('img')).toHaveLength(2)
  expect(
    screen.getAllByRole('region', {
      name: 'Model consumption records (page sample)',
    })
  ).toHaveLength(1)
  expect(
    screen.getAllByRole('region', { name: 'Token directions (page sample)' })
  ).toHaveLength(1)
  expect(
    screen.getByRole('table', { name: 'Recent consumption records' })
  ).toBeVisible()
})
