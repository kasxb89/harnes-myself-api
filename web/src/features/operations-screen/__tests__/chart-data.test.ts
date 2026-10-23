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
import { describe, expect, it } from 'vitest'

import { mediaPayload } from '@/features/media-operations/__tests__/fixtures'
import type { ModelProfitResult } from '@/features/model-profits/types'
import { usageLogSchema } from '@/features/usage-logs/data/schema'
import type { ClientActivitySession } from '@/features/user-activity/types'

import type { OperationsSnapshot } from '../api'
import { operationsChartData } from '../chart-data'

const window = { start_timestamp: 100, end_timestamp: 200 }
const totals = {
  requests: 7,
  billing_events: 7,
  revenue_cny: 10,
  known_cost_revenue_cny: 4,
  upstream_cost_cny: 2,
  gross_profit_cny: 2,
  profit_margin: 0.5,
  known_cost_requests: 2,
  unknown_cost_requests: 5,
  known_cost_events: 2,
  unknown_cost_events: 5,
  pending_requests: 3,
  pending_billing_events: 3,
  pending_revenue_cny: 3,
  refund_cny: 0,
  cost_coverage: 2 / 7,
}
const profitReport: ModelProfitResult = {
  rows: [
    {
      ...totals,
      model_name: 'refund-model',
      revenue_cny: -2,
      upstream_cost_cny: -1,
    },
    {
      ...totals,
      model_name: 'unknown-cost',
      revenue_cny: 0,
      upstream_cost_cny: null,
    },
  ],
  summary: totals,
  timezone: 'Asia/Shanghai',
  currency: 'CNY',
  ...window,
  funding_collection_status: 'not_collected',
}
const log = usageLogSchema.parse({
  id: 1,
  user_id: 7,
  type: 2,
  created_at: 150,
  content: '',
  model_name: 'model-a',
  prompt_tokens: 10,
  completion_tokens: 4,
  quota: 999999,
})
const usageSnapshot: Extract<OperationsSnapshot, { tab: 'usage' }> = {
  tab: 'usage',
  window,
  logs: { items: [log, { ...log, id: 2 }], total: 123 },
  stats: { quota: 10000, rpm: 200, tpm: 300 },
}
const pool = {
  media_kind: 'video' as const,
  channel_ids: [1],
  pool_id: 'pool',
  label: 'Pool',
  enabled: true,
  reconciled: true,
  configured_limit: 4,
  effective_limit: 4,
  known_upstream_limit: null,
  occupied: 6,
  unknown: 2,
  available_slots: 0,
}
const concurrencySnapshot: Extract<OperationsSnapshot, { tab: 'concurrency' }> =
  {
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
const client: ClientActivitySession = {
  id: 1,
  user_id: 7,
  username: 'operator',
  session_id: 'p1',
  platform: 'darwin',
  app_version: '1',
  started_at: 100,
  last_seen_at: 200,
  ended_at: 0,
  online_seconds: 100,
  foreground_seconds: 50,
  foreground: true,
  online: true,
  status: 'foreground',
  model_calls: 0,
  model_errors: 0,
  last_model_at: 0,
}
const activitySnapshot: Extract<OperationsSnapshot, { tab: 'activity' }> = {
  tab: 'activity',
  window,
  sessions: {
    items: [client, { ...client, id: 2, status: 'background' }],
    total: 99,
  },
  summary: {
    server_time: 200,
    heartbeat_interval_seconds: 30,
    online_timeout_seconds: 90,
    online_users: 1,
    foreground_users: 1,
    started_sessions: 2,
    active_users: 1,
    online_seconds: 100,
    foreground_seconds: 50,
    model_calls: 0,
    model_errors: 0,
  },
}

describe('operations chart observations', () => {
  it('returns two independent charts for all six tabs without fetching or inventing trends', () => {
    const snapshots: OperationsSnapshot[] = [
      { tab: 'profit', window, report: profitReport },
      concurrencySnapshot,
      usageSnapshot,
      { tab: 'errors', window, logs: usageSnapshot.logs },
      { tab: 'video', window, report: mediaPayload },
      activitySnapshot,
    ]
    for (const snapshot of snapshots) {
      const charts = operationsChartData(snapshot)
      expect(charts).toHaveLength(2)
      expect(new Set(charts.map((chart) => chart.id)).size).toBe(2)
      expect(
        charts.every((chart) => chart.kind === 'bar' || chart.kind === 'donut')
      ).toBe(true)
    }
  })
  it('preserves negative CNY and true zero charges while missing costs and all-unknown Other costs stay null', () => {
    const [models] = operationsChartData({
      tab: 'profit',
      window,
      report: profitReport,
    })
    expect(models.unit).toBe('cny')
    expect(models.rows).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ value: -2, secondary: -1 }),
        expect.objectContaining({ value: 0, secondary: null }),
      ])
    )
    expect(models.series.map((series) => series.labelKey)).toEqual([
      'Balance charges',
      'Known model procurement cost (estimate)',
    ])
    const rows = Array.from({ length: 10 }, (_, index) => ({
      ...totals,
      model_name: `model-${index}`,
      revenue_cny: 10 - index,
      upstream_cost_cny: index < 8 ? 1 : null,
    }))
    const other = operationsChartData({
      tab: 'profit',
      window,
      report: { ...profitReport, rows },
    })[0].rows.at(-1)
    expect(other).toMatchObject({ id: 'other', value: 3, secondary: null })
  })
  it('partitions pending evidence out of unknown events and rejects missing or inconsistent event counts', () => {
    const evidence = operationsChartData({
      tab: 'profit',
      window,
      report: profitReport,
    })[1]
    expect(evidence.rows.map((row) => row.value)).toEqual([2, 2, 3])
    expect(evidence.rows.reduce((sum, row) => sum + (row.value ?? 0), 0)).toBe(
      7
    )
    for (const summary of [
      { ...totals, pending_billing_events: undefined },
      { ...totals, pending_billing_events: 6 },
      { ...totals, billing_events: 8 },
    ]) {
      expect(
        operationsChartData({
          tab: 'profit',
          window,
          report: { ...profitReport, summary },
        })[1].rows
      ).toEqual([])
    }
  })
  it('keeps occupied slots over policy limits and treats UNKNOWN as a subset with absent provider capacity', () => {
    const [limits, held] = operationsChartData(concurrencySnapshot)
    expect(limits.rows[0]).toMatchObject({
      value: 6,
      secondary: 4,
      tertiary: null,
    })
    expect(limits.unknownPools).toBe(1)
    expect(held.rows.map((row) => row.value)).toEqual([4, 2])
    expect(
      operationsChartData({
        ...concurrencySnapshot,
        report: {
          ...concurrencySnapshot.report,
          pools: [{ ...pool, unknown: 7 }],
        },
      })[1].rows
    ).toEqual([])
  })
  it('counts only returned consumption records and tokens rather than the total or rolling summary', () => {
    const [models, tokens] = operationsChartData(usageSnapshot)
    expect(models.sample).toEqual({ kind: 'logs', shown: 2, total: 123 })
    expect(models.rows[0].value).toBe(2)
    expect(tokens.rows.map((row) => row.value)).toEqual([20, 8])
    expect([models.unit, tokens.unit]).toEqual(['records', 'tokens'])
  })
  it('charts recorded gateway mappings separately from upstream HTTP evidence and does not infer missing status', () => {
    const snapshot: OperationsSnapshot = {
      tab: 'errors',
      window,
      logs: {
        total: 123,
        items: [
          {
            ...log,
            type: 5,
            content: 'unclassified failure',
            other: JSON.stringify({
              status_code: 502,
              status_code_origin: 'gateway_task_mapping',
              upstream_http_status: 429,
            }),
          },
          { ...log, id: 2, type: 5, content: 'HTTP 429', other: '' },
        ],
      },
    }
    const [reasons, statuses] = operationsChartData(snapshot)
    expect(reasons.rows.reduce((sum, row) => sum + (row.value ?? 0), 0)).toBe(2)
    expect(statuses.rows).toEqual([
      expect.objectContaining({ label: 'HTTP 502', value: 1 }),
      expect.objectContaining({ label: 'Unknown', value: 1 }),
    ])
    expect(statuses.sample?.total).toBe(123)
  })
  it('uses real persisted states and source-specific media errors without assigning unknown capacity', () => {
    const [states, stages] = operationsChartData({
      tab: 'video',
      window,
      report: mediaPayload,
    })
    expect(states.rows.map((row) => row.value)).toEqual([1, 3, 2, 4, 5])
    expect(states.unit).toBe('tasks')
    expect(stages.rows).toEqual([
      expect.objectContaining({
        id: 'task_terminal',
        value: 8,
        secondary: null,
      }),
      expect.objectContaining({ id: 'preflight', value: null, secondary: 4 }),
    ])
    expect(states.series).toHaveLength(1)
  })
  it('counts returned client processes without confusing repeated users or missing runtime/workload with zero', () => {
    const [platforms, states] = operationsChartData(activitySnapshot)
    expect(platforms.rows[0].value).toBe(2)
    expect(platforms.sample).toEqual({ kind: 'clients', shown: 2, total: 99 })
    expect(states.rows.map((row) => row.value)).toEqual([1, 1, 0])
    expect(platforms.unit).toBe('processes')
  })
})
