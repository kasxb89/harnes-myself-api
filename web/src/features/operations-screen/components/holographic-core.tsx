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
import { useId } from 'react'
import { useTranslation } from 'react-i18next'

import {
  formatProfitCny,
  verifiedCashAmount,
} from '@/features/model-profits/lib/format'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import type { OperationsSnapshot } from '../api'

import './holographic-core.css'

interface CoreStat {
  labelKey: string
  value: number | null
  currency?: boolean
}
interface CoreObservation {
  primary: CoreStat
  stats: CoreStat[]
  noteKey?: string
}
function count(value: unknown): number | null {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0
    ? value
    : null
}
function countSum(values: number[]): number | null {
  if (!values.length || values.some((value) => count(value) === null)) {
    return null
  }
  return count(values.reduce((sum, value) => sum + value, 0))
}
function observation(snapshot: OperationsSnapshot): CoreObservation {
  switch (snapshot.tab) {
    case 'profit':
      return {
        primary: {
          labelKey: 'Usage count',
          value: count(snapshot.report.summary.requests),
        },
        stats: [
          {
            labelKey: 'Cash profit',
            value: verifiedCashAmount(
              snapshot.report.summary,
              'cash_profit_cny'
            ),
            currency: true,
          },
          {
            labelKey: 'Known model procurement cost (estimate)',
            value: snapshot.report.summary.upstream_cost_cny,
            currency: true,
          },
        ],
      }
    case 'concurrency':
      return {
        primary: {
          labelKey: 'Occupied admission slots',
          value: countSum(snapshot.report.pools.map((pool) => pool.occupied)),
        },
        stats: [
          {
            labelKey: 'Registered account pools',
            value: snapshot.report.pools.length,
          },
          {
            labelKey: 'UNKNOWN slots held',
            value: countSum(snapshot.report.pools.map((pool) => pool.unknown)),
          },
        ],
        noteKey:
          'Admission slots are gateway observations, not exact upstream running concurrency. Unknown slots remain occupied. Current pools do not follow the selected history window.',
      }
    case 'usage':
      return {
        primary: {
          labelKey: 'Consumption records in range',
          value: count(snapshot.logs.total),
        },
        stats: [
          {
            labelKey: 'Consumption records in the last 60 seconds',
            value: count(snapshot.stats.rpm),
          },
          {
            labelKey: 'Tokens recorded in the last 60 seconds',
            value: count(snapshot.stats.tpm),
          },
        ],
        noteKey:
          'Consumption records follow recording or settlement time. Missing or delayed records are not a complete live count of requests in flight.',
      }
    case 'errors':
      return {
        primary: {
          labelKey: 'API error records in range',
          value: count(snapshot.logs.total),
        },
        stats: [],
        noteKey:
          'Gateway error records can include multiple upstream retry attempts for one request. Record counts are not failed-request counts or an API failure rate. Client-only reports are not included.',
      }
    case 'image':
    case 'video':
      return {
        primary: {
          labelKey: 'Current task records',
          value: countSum([
            snapshot.report.summary.not_start,
            snapshot.report.summary.submitted,
            snapshot.report.summary.queued,
            snapshot.report.summary.in_progress,
            snapshot.report.summary.unknown,
          ]),
        },
        stats: [
          {
            labelKey: 'Submitted or running',
            value: countSum([
              snapshot.report.summary.submitted,
              snapshot.report.summary.in_progress,
            ]),
          },
          {
            labelKey: 'Upstream-reported queued',
            value: count(snapshot.report.summary.queued),
          },
        ],
        noteKey:
          'Persisted task states are observations, not a complete live queue. Current task states are global; historical errors and accepted tasks follow the query window.',
      }
    case 'activity':
      return {
        primary: {
          labelKey: 'Online client processes',
          value: count(snapshot.summary.runtime_sessions),
        },
        stats: [
          {
            labelKey: 'Currently online users',
            value: count(snapshot.summary.online_users),
          },
          {
            labelKey: 'Active foreground users',
            value: count(snapshot.summary.foreground_users),
          },
        ],
        noteKey:
          'Current online counts and workload are global and do not follow the user or date filters.',
      }
  }
}

export function HolographicCore({
  snapshot,
  paused,
  stale = false,
}: {
  snapshot?: OperationsSnapshot
  paused: boolean
  stale?: boolean
}) {
  const { t, i18n } = useTranslation()
  const labelId = useId()
  const noteId = useId()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const data = snapshot ? observation(snapshot) : undefined
  const format = (stat: CoreStat) => {
    if (stat.value === null || !Number.isFinite(stat.value)) {
      return stat.currency ? t('Pending confirmation') : t('Unknown')
    }
    return stat.currency
      ? formatProfitCny(stat.value, locale)
      : formatNumber(stat.value, locale)
  }
  let status = paused ? 'Paused' : 'Current observation'
  if (stale) status = 'Stale sample'
  if (!snapshot) status = 'Awaiting data'
  return (
    <section
      className='ops-holo-core'
      data-paused={paused || stale || !snapshot ? 'true' : 'false'}
      data-stale={stale && snapshot ? 'true' : 'false'}
      data-observed={Boolean(snapshot)}
      data-tab={snapshot?.tab}
      data-secondary-count={data?.stats.length ?? 0}
      aria-labelledby={labelId}
      aria-describedby={snapshot?.tab === 'profit' ? undefined : noteId}
    >
      <div className='ops-holo-stage'>
        <div className='ops-holo-accent' aria-hidden='true' />
        <div className='ops-holo-readout'>
          <h2 id={labelId} className='ops-holo-label'>
            {t(data?.primary.labelKey ?? 'No successful sample yet')}
          </h2>
          <output className='ops-holo-value' aria-labelledby={labelId}>
            {data ? format(data.primary) : '—'}
          </output>
          <p className='ops-holo-eyebrow'>{t(status)}</p>
          <div className='ops-holo-readout__rule' aria-hidden='true'>
            <i />
            <i />
            <i />
          </div>
        </div>
      </div>
      {data && data.stats.length > 0 && (
        <dl className='ops-holo-telemetry'>
          {data.stats.map((stat) => (
            <div key={stat.labelKey} className='ops-holo-stat'>
              <dt>{t(stat.labelKey)}</dt>
              <dd>{format(stat)}</dd>
            </div>
          ))}
        </dl>
      )}
      {snapshot?.tab !== 'profit' && (
        <p id={noteId} className='ops-holo-note'>
          {t(data?.noteKey ?? 'No chart observations')}
        </p>
      )}
    </section>
  )
}
