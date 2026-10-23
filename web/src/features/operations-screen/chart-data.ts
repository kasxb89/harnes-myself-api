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
import { stageLabels } from '@/features/media-operations/labels'
import {
  getErrorHttpStatus,
  parseErrorDiagnostics,
  type ErrorReason,
} from '@/features/usage-logs/lib/error-diagnostics'
import { parseLogOther } from '@/features/usage-logs/lib/format'

import type { OperationsSnapshot } from './api'

export interface ScienceChartRow {
  id: string
  label: string
  translate?: boolean
  errorReason?: ErrorReason
  value: number | null
  secondary?: number | null
  tertiary?: number | null
}
export interface ScienceChartSeries {
  key: 'value' | 'secondary' | 'tertiary'
  labelKey: string
  color: 'cyan' | 'blue' | 'magenta' | 'amber' | 'green'
}
export interface ScienceChartData {
  id: string
  kind: 'bar' | 'donut'
  titleKey: string
  noteKey: string
  noteValues?: Record<string, number>
  unit:
    | 'cny'
    | 'records'
    | 'events'
    | 'tokens'
    | 'slots'
    | 'processes'
    | 'tasks'
  categoryKey: string
  series: ScienceChartSeries[]
  rows: ScienceChartRow[]
  emptyKey?: string
  unknownPools?: number
  sample?: { kind: 'logs' | 'clients'; shown: number; total: number }
}
const countSeries: ScienceChartSeries[] = [
  { key: 'value', labelKey: 'Count', color: 'cyan' },
]
const logSample =
  'Chart sample: {{shown}} returned log records of {{total}} matches. Settlement records are not distinct API calls.'
const clientSample =
  'Chart sample: {{shown}} returned client processes of {{total}} matches. Current presence is global, not a history-window total.'
const isCount = (value: unknown): value is number =>
  typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
const knownSum = (values: (number | null | undefined)[]): number | null => {
  const known = values.filter(
    (value): value is number =>
      typeof value === 'number' && Number.isFinite(value)
  )
  return known.length ? known.reduce((sum, value) => sum + value, 0) : null
}
function groupChartRows(
  rows: ScienceChartRow[],
  limit: number,
  other: string
): ScienceChartRow[] {
  const sorted = [...rows].sort(
    (a, b) =>
      (b.value ?? -Infinity) - (a.value ?? -Infinity) ||
      a.label.localeCompare(b.label)
  )
  if (sorted.length <= limit) return sorted
  const tail = sorted.slice(limit)
  return [
    ...sorted.slice(0, limit),
    {
      id: 'other',
      label: other,
      translate: true,
      value: knownSum(tail.map((row) => row.value)),
      secondary: knownSum(tail.map((row) => row.secondary)),
    },
  ]
}
function countCategories(
  values: string[],
  unknownLabel: string
): ScienceChartRow[] {
  const counts = new Map<string, number>()
  for (const value of values) counts.set(value, (counts.get(value) ?? 0) + 1)
  return [...counts].map(([label, value]) => ({
    id: label || 'unknown',
    label: label || unknownLabel,
    translate: !label,
    value,
  }))
}

export function operationsChartData(
  snapshot: OperationsSnapshot
): ScienceChartData[] {
  switch (snapshot.tab) {
    case 'profit': {
      const report = snapshot.report
      const rows = groupChartRows(
        report.rows.map((row) => ({
          id: row.model_name || 'unknown',
          label: row.model_name || 'Unknown model',
          translate: !row.model_name,
          value: Number.isFinite(row.revenue_cny) ? row.revenue_cny : null,
          secondary:
            typeof row.upstream_cost_cny === 'number' &&
            Number.isFinite(row.upstream_cost_cny)
              ? row.upstream_cost_cny
              : null,
        })),
        8,
        'Other models'
      )
      const totals = report.summary
      const known = totals.known_cost_events
      const unknown = totals.unknown_cost_events
      const pending = totals.pending_billing_events
      const valid =
        isCount(known) &&
        isCount(unknown) &&
        isCount(pending) &&
        pending <= unknown &&
        (totals.billing_events === undefined ||
          (isCount(totals.billing_events) &&
            known + unknown === totals.billing_events))
      return [
        {
          id: 'profit-models',
          kind: 'bar',
          titleKey: 'Model balance charges and known costs',
          noteKey:
            'Top {{shown}} of {{total}} models; remaining models are grouped as Other models. Known costs can be partial. Cash profit is not plotted without funding evidence.',
          noteValues: {
            shown: Math.min(8, report.rows.length),
            total: report.rows.length,
          },
          unit: 'cny',
          categoryKey: 'Model',
          series: [
            { key: 'value', labelKey: 'Balance charges', color: 'cyan' },
            {
              key: 'secondary',
              labelKey: 'Known model procurement cost (estimate)',
              color: 'magenta',
            },
          ],
          rows,
        },
        {
          id: 'profit-evidence',
          kind: 'donut',
          titleKey: 'Billing cost evidence',
          noteKey:
            'Known, unknown excluding pending, and pending billing events are disjoint. These do not identify paid or gift funding.',
          unit: 'events',
          categoryKey: 'Status',
          series: countSeries,
          emptyKey: 'Cost evidence is unavailable for this response.',
          rows: valid
            ? [
                {
                  id: 'known',
                  label: 'Known cost events',
                  translate: true,
                  value: known,
                },
                {
                  id: 'unknown',
                  label: 'Unknown excluding pending',
                  translate: true,
                  value: unknown - pending,
                },
                {
                  id: 'pending',
                  label: 'Pending cost evidence',
                  translate: true,
                  value: pending,
                },
              ]
            : [],
        },
      ]
    }
    case 'concurrency': {
      const pools = snapshot.report.pools
      const occupied = pools.reduce((sum, pool) => sum + pool.occupied, 0)
      const unknown = pools.reduce((sum, pool) => sum + pool.unknown, 0)
      const valid = pools.every(
        (pool) =>
          isCount(pool.occupied) &&
          isCount(pool.unknown) &&
          pool.unknown <= pool.occupied
      )
      return [
        {
          id: 'concurrency-pools',
          kind: 'bar',
          titleKey: 'Account pool occupancy and limits',
          noteKey:
            'Top {{shown}} of {{total}} account pools by occupied slots. Limits are gateway policies; unknown upstream limits have no bar.',
          noteValues: {
            shown: Math.min(10, pools.length),
            total: pools.length,
          },
          unit: 'slots',
          categoryKey: 'Shared upstream account',
          unknownPools: pools.filter(
            (pool) => pool.known_upstream_limit === null
          ).length,
          series: [
            {
              key: 'value',
              labelKey: 'Occupied admission slots',
              color: 'cyan',
            },
            {
              key: 'secondary',
              labelKey: 'Effective admission limit',
              color: 'blue',
            },
            {
              key: 'tertiary',
              labelKey: 'Known upstream limit',
              color: 'magenta',
            },
          ],
          rows: [...pools]
            .sort(
              (a, b) =>
                b.occupied - a.occupied || a.label.localeCompare(b.label)
            )
            .slice(0, 10)
            .map((pool) => ({
              id: pool.pool_id,
              label: pool.label,
              value: pool.occupied,
              secondary: pool.effective_limit,
              tertiary: pool.known_upstream_limit,
            })),
        },
        {
          id: 'concurrency-held',
          kind: 'donut',
          titleKey: 'Occupied slot observations',
          noteKey:
            'UNKNOWN is already included in occupied slots. Over-limit occupancy is preserved; no utilization percentage is inferred.',
          unit: 'slots',
          categoryKey: 'Status',
          series: countSeries,
          emptyKey: valid
            ? 'No chart observations'
            : 'Slot counts are inconsistent; no occupancy share is inferred.',
          rows:
            valid && pools.length
              ? [
                  {
                    id: 'known',
                    label: 'Other occupied slots',
                    translate: true,
                    value: occupied - unknown,
                  },
                  {
                    id: 'unknown',
                    label: 'UNKNOWN slots held',
                    translate: true,
                    value: unknown,
                  },
                ]
              : [],
        },
      ]
    }
    case 'usage': {
      const items = snapshot.logs.items
      const sample = {
        kind: 'logs' as const,
        shown: items.length,
        total: snapshot.logs.total,
      }
      const sumTokens = (field: 'prompt_tokens' | 'completion_tokens') =>
        items.every((row) => isCount(row[field]))
          ? items.reduce((sum, row) => sum + row[field], 0)
          : null
      return [
        {
          id: 'usage-models',
          kind: 'bar',
          titleKey: 'Model consumption records (page sample)',
          noteKey: logSample,
          unit: 'records',
          categoryKey: 'Model',
          series: countSeries,
          sample,
          rows: groupChartRows(
            countCategories(
              items.map((row) => row.model_name),
              'Unknown model'
            ),
            8,
            'Other models'
          ),
        },
        {
          id: 'usage-tokens',
          kind: 'donut',
          titleKey: 'Token directions (page sample)',
          noteKey:
            'Tokens summed from this returned page only; not the full history window or the rolling 60-second summary.',
          unit: 'tokens',
          categoryKey: 'Type',
          series: countSeries,
          sample,
          rows: items.length
            ? [
                {
                  id: 'input',
                  label: 'Input tokens',
                  translate: true,
                  value: sumTokens('prompt_tokens'),
                },
                {
                  id: 'output',
                  label: 'Output tokens',
                  translate: true,
                  value: sumTokens('completion_tokens'),
                },
              ]
            : [],
        },
      ]
    }
    case 'errors': {
      const sample = {
        kind: 'logs' as const,
        shown: snapshot.logs.items.length,
        total: snapshot.logs.total,
      }
      const reasons = new Map<ErrorReason, number>()
      const statuses = new Map<string, number>()
      for (const row of snapshot.logs.items) {
        const other = parseLogOther(row.other)
        const reason =
          parseErrorDiagnostics(row.content, other)?.reason ?? 'unknown'
        reasons.set(reason, (reasons.get(reason) ?? 0) + 1)
        const status = getErrorHttpStatus(other).recorded
        const key =
          typeof status === 'number' &&
          Number.isInteger(status) &&
          status >= 100 &&
          status <= 599
            ? `HTTP ${status}`
            : ''
        statuses.set(key, (statuses.get(key) ?? 0) + 1)
      }
      return [
        {
          id: 'error-reasons',
          kind: 'bar',
          titleKey: 'Error categories (page sample)',
          noteKey:
            'Error record categories on this page only. Retry attempts may share a request; this is not a failure rate.',
          unit: 'records',
          categoryKey: 'Error reason',
          series: countSeries,
          sample,
          rows: [...reasons]
            .sort((a, b) => b[1] - a[1])
            .map(([reason, value]) => ({
              id: reason,
              label: reason,
              errorReason: reason,
              value,
            })),
        },
        {
          id: 'error-statuses',
          kind: 'donut',
          titleKey: 'Recorded HTTP statuses (page sample)',
          noteKey:
            'Recorded HTTP codes can be gateway task mappings. Missing codes remain unknown; they are not inferred from error text.',
          unit: 'records',
          categoryKey: 'Recorded status',
          series: countSeries,
          sample,
          rows: [...statuses].map(([label, value]) => ({
            id: label || 'unknown',
            label: label || 'Unknown',
            translate: !label,
            value,
          })),
        },
      ]
    }
    case 'image':
    case 'video': {
      const report = snapshot.report
      const stages = new Map<string, ScienceChartRow>()
      for (const entry of report.history_summary.stages) {
        const row = stages.get(entry.stage) ?? {
          id: entry.stage,
          label: stageLabels[entry.stage],
          translate: true,
          value: null,
          secondary: null,
        }
        const key = entry.source === 'gateway' ? 'value' : 'secondary'
        row[key] = (row[key] ?? 0) + entry.events
        stages.set(entry.stage, row)
      }
      return [
        {
          id: 'media-states',
          kind: 'donut',
          titleKey:
            snapshot.tab === 'image'
              ? 'Persisted image task states'
              : 'Persisted video task states',
          noteKey:
            'Current persisted states are global observations and do not follow the history window.',
          unit: 'tasks',
          categoryKey: 'Status',
          series: countSeries,
          rows: [
            {
              id: 'not-start',
              label: 'Recorded not started',
              translate: true,
              value: report.summary.not_start,
            },
            {
              id: 'submitted',
              label: 'Submitted',
              translate: true,
              value: report.summary.submitted,
            },
            {
              id: 'queued',
              label: 'Upstream-reported queued',
              translate: true,
              value: report.summary.queued,
            },
            {
              id: 'running',
              label: 'In progress',
              translate: true,
              value: report.summary.in_progress,
            },
            {
              id: 'unknown',
              label: 'Recorded unknown states',
              translate: true,
              value: report.summary.unknown,
            },
          ],
        },
        {
          id: 'media-stages',
          kind: 'bar',
          titleKey:
            snapshot.tab === 'image'
              ? 'Image error stages'
              : 'Video error stages',
          noteKey:
            'Historical error events by recorded stage and source in the selected window; not distinct failed requests.',
          unit: 'records',
          categoryKey: 'Stage',
          rows: [...stages.values()],
          series: [
            { key: 'value', labelKey: 'Gateway record', color: 'cyan' },
            { key: 'secondary', labelKey: 'Client report', color: 'magenta' },
          ],
        },
      ]
    }
    case 'activity': {
      const items = snapshot.sessions.items
      const sample = {
        kind: 'clients' as const,
        shown: items.length,
        total: snapshot.sessions.total,
      }
      return [
        {
          id: 'activity-platforms',
          kind: 'bar',
          titleKey: 'Client platforms (page sample)',
          noteKey: clientSample,
          unit: 'processes',
          categoryKey: 'Platform',
          series: countSeries,
          sample,
          rows: groupChartRows(
            countCategories(
              items.map((item) => item.platform),
              'Unknown'
            ),
            8,
            'Other'
          ),
        },
        {
          id: 'activity-states',
          kind: 'donut',
          titleKey: 'Client process states (page sample)',
          noteKey:
            'Client state counts describe reported processes, not distinct people. Clients without reporting are outside this sample.',
          unit: 'processes',
          categoryKey: 'Status',
          series: countSeries,
          sample,
          rows: items.length
            ? [
                {
                  id: 'foreground',
                  label: 'Active foreground',
                  translate: true,
                  value: items.filter((item) => item.status === 'foreground')
                    .length,
                },
                {
                  id: 'background',
                  label: 'Background or idle',
                  translate: true,
                  value: items.filter((item) => item.status === 'background')
                    .length,
                },
                {
                  id: 'offline',
                  label: 'Offline',
                  translate: true,
                  value: items.filter((item) => item.status === 'offline')
                    .length,
                },
              ]
            : [],
        },
      ]
    }
  }
}
