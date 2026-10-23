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
import { render, screen } from '@testing-library/react'
import { expect, it } from 'vitest'

import { mediaPayload } from '@/features/media-operations/__tests__/fixtures'
import type { ModelProfitResult } from '@/features/model-profits/types'
import type { ClientActivitySummary } from '@/features/user-activity/types'

import type { OperationsSnapshot } from '../api'
import { HolographicCore } from '../components/holographic-core'

const window = { start_timestamp: 100, end_timestamp: 200 }
const totals = {
  requests: 3,
  billing_events: 99,
  revenue_cny: 10,
  known_cost_revenue_cny: 4,
  upstream_cost_cny: -1,
  gross_profit_cny: 12,
  profit_margin: 1.2,
  known_cost_requests: 2,
  unknown_cost_requests: 1,
  known_cost_events: 20,
  unknown_cost_events: 79,
  pending_requests: 0,
  pending_billing_events: 0,
  pending_revenue_cny: 0,
  refund_cny: 0,
  cost_coverage: 2 / 3,
  cash_profit_cny: 0,
  funding_status: 'unattributed',
  funding_coverage: 0,
  funding_unknown_events: 99,
}
const report: ModelProfitResult = {
  rows: [],
  summary: totals,
  timezone: 'Asia/Shanghai',
  currency: 'CNY',
  ...window,
  funding_collection_status: 'not_collected',
}
const summary: ClientActivitySummary = {
  server_time: 200,
  heartbeat_interval_seconds: 30,
  online_timeout_seconds: 90,
  online_users: 3,
  foreground_users: 1,
  runtime_sessions: 7,
  started_sessions: 20,
  active_users: 4,
  online_seconds: 100,
  foreground_seconds: 50,
  model_calls: 100,
  model_errors: 2,
}
const activity: Extract<OperationsSnapshot, { tab: 'activity' }> = {
  tab: 'activity',
  window,
  sessions: { items: [], total: 99 },
  summary,
}
const pool = {
  pool_id: 'pool',
  label: 'Pool',
  media_kind: 'video' as const,
  channel_ids: [1],
  enabled: true,
  reconciled: true,
  configured_limit: 4,
  effective_limit: 4,
  known_upstream_limit: null,
  occupied: 6,
  unknown: 2,
  available_slots: 0,
}
const concurrency: Extract<OperationsSnapshot, { tab: 'concurrency' }> = {
  tab: 'concurrency',
  window,
  report: {
    schema_version: 1,
    revision: 'r1',
    defaults: { video: 2, image: 4 },
    pool_overrides: [],
    pools: [pool],
  },
}

it('shows original calls rather than billing events while preserving negative CNY cost and unverified cash profit', () => {
  render(
    <HolographicCore
      snapshot={{ tab: 'profit', window, report }}
      paused={false}
    />
  )
  expect(screen.getByRole('status', { name: 'Usage count' })).toHaveTextContent(
    '3'
  )
  expect(screen.getByText('Cash profit').nextElementSibling).toHaveTextContent(
    'Pending confirmation'
  )
  expect(
    screen.getByText('Known model procurement cost (estimate)')
      .nextElementSibling
  ).toHaveTextContent('-CN¥1.00')
  expect(
    screen.getByText(
      'Paid-versus-gift funding allocations are not collected in this version. New consumption also remains unverified until a funding ledger is implemented.'
    )
  ).toBeVisible()
})
it('preserves verified negative cash profit and genuine zero calls instead of converting them into unknown or random animated digits', () => {
  render(
    <HolographicCore
      snapshot={{
        tab: 'profit',
        window,
        report: {
          ...report,
          summary: {
            ...totals,
            requests: 0,
            unknown_cost_events: 0,
            funding_status: 'attributed',
            funding_coverage: 1,
            funding_unknown_events: 0,
            cash_profit_cny: -2,
          },
        },
      }}
      paused={false}
    />
  )
  expect(screen.getByRole('status', { name: 'Usage count' })).toHaveTextContent(
    '0'
  )
  expect(screen.getByText('Cash profit').nextElementSibling).toHaveTextContent(
    '-CN¥2.00'
  )
})
it('uses occupied slots directly and does not add UNKNOWN again or invent availability from absent pools', () => {
  const { rerender } = render(
    <HolographicCore snapshot={concurrency} paused={false} />
  )
  expect(
    screen.getByRole('status', { name: 'Occupied admission slots' })
  ).toHaveTextContent('6')
  expect(
    screen.getByText('UNKNOWN slots held').nextElementSibling
  ).toHaveTextContent('2')
  rerender(
    <HolographicCore
      snapshot={{
        ...concurrency,
        report: { ...concurrency.report, pools: [] },
      }}
      paused={false}
    />
  )
  expect(
    screen.getByRole('status', { name: 'Occupied admission slots' })
  ).toHaveTextContent('Unknown')
  expect(
    screen.getByText('Registered account pools').nextElementSibling
  ).toHaveTextContent('0')
})
it('shows total matching usage and error records without promoting returned-page or rolling counts to full-window calls', () => {
  const { rerender } = render(
    <HolographicCore
      snapshot={{
        tab: 'usage',
        window,
        logs: { items: [], total: 123 },
        stats: { quota: 100, rpm: 6, tpm: 1000 },
      }}
      paused={false}
    />
  )
  expect(
    screen.getByRole('status', { name: 'Consumption records in range' })
  ).toHaveTextContent('123')
  expect(
    screen.getByText('Consumption records in the last 60 seconds')
      .nextElementSibling
  ).toHaveTextContent('6')
  rerender(
    <HolographicCore
      snapshot={{ tab: 'errors', window, logs: { items: [], total: 42 } }}
      paused={false}
    />
  )
  expect(
    screen.getByRole('status', { name: 'API error records in range' })
  ).toHaveTextContent('42')
  expect(
    screen.getByText(
      /Record counts are not failed-request counts or an API failure rate/
    )
  ).toBeVisible()
})
it('counts the five observed persisted media states without implying an upstream capacity or complete live queue', () => {
  render(
    <HolographicCore
      snapshot={{ tab: 'video', window, report: mediaPayload }}
      paused={false}
    />
  )
  expect(
    screen.getByRole('status', { name: 'Current task records' })
  ).toHaveTextContent('15')
  expect(
    screen.getByText('Submitted or running').nextElementSibling
  ).toHaveTextContent('7')
  expect(screen.getByText(/not a complete live queue/)).toBeVisible()
})
it('uses online process counts instead of users and preserves missing runtime counts as unknown', () => {
  const { rerender } = render(
    <HolographicCore snapshot={activity} paused={false} />
  )
  expect(
    screen.getByRole('status', { name: 'Online client processes' })
  ).toHaveTextContent('7')
  expect(
    screen.getByText('Currently online users').nextElementSibling
  ).toHaveTextContent('3')
  rerender(
    <HolographicCore
      snapshot={{
        ...activity,
        summary: { ...summary, runtime_sessions: undefined },
      }}
      paused={false}
    />
  )
  expect(
    screen.getByRole('status', { name: 'Online client processes' })
  ).toHaveTextContent('Unknown')
})
it('marks stale retained observations and pauses decoration without retaining a count after the authorized snapshot is removed', () => {
  const { container, rerender } = render(
    <HolographicCore snapshot={activity} paused={false} stale />
  )
  expect(screen.getByText('Stale sample')).toBeVisible()
  expect(screen.queryByText('Current observation')).not.toBeInTheDocument()
  expect(container.querySelector('.ops-holo-core')).toHaveAttribute(
    'data-paused',
    'true'
  )
  expect(container.querySelector('svg')).toHaveAttribute('aria-hidden', 'true')
  rerender(<HolographicCore paused />)
  expect(screen.getByText('Awaiting data')).toBeVisible()
  expect(
    screen.getByRole('status', { name: 'No successful sample yet' })
  ).toHaveTextContent('—')
  expect(screen.queryByText('7')).not.toBeInTheDocument()
  expect(container.querySelector('.ops-holo-core')).toHaveAttribute(
    'data-observed',
    'false'
  )
})

it('keeps an unobserved or first-failed core awaiting data with frozen decoration until a successful sample arrives', () => {
  const { container, rerender } = render(<HolographicCore paused={false} />)
  expect(screen.getByText('Awaiting data')).toBeVisible()
  expect(screen.queryByText('Current observation')).not.toBeInTheDocument()
  expect(container.querySelector('.ops-holo-core')).toHaveAttribute(
    'data-paused',
    'true'
  )
  rerender(<HolographicCore paused={false} stale />)
  expect(screen.getByText('Awaiting data')).toBeVisible()
  expect(screen.queryByText('Stale sample')).not.toBeInTheDocument()
  expect(container.querySelector('.ops-holo-core')).toHaveAttribute(
    'data-stale',
    'false'
  )
  rerender(<HolographicCore snapshot={activity} paused={false} />)
  expect(screen.getByText('Current observation')).toBeVisible()
  expect(
    screen.getByRole('status', { name: 'Online client processes' })
  ).toHaveTextContent('7')
  expect(container.querySelector('.ops-holo-core')).toHaveAttribute(
    'data-paused',
    'false'
  )
})
