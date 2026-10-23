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
  noteKey: string
  fundingNotCollected?: boolean
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
        noteKey:
          'Usage count means original calls. Unit prices are period averages per call after settlement and refunds, not current token, second or image tariffs. Pending, incomplete or refund-only periods remain unconfirmed.',
        fundingNotCollected:
          snapshot.report.funding_collection_status === 'not_collected',
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

function InkLandscapeArtwork() {
  const id = useId().replaceAll(':', '')
  return (
    <svg
      className='ops-holo-core__art'
      viewBox='0 0 600 600'
      aria-hidden='true'
      focusable='false'
    >
      <defs>
        <linearGradient id={`${id}-mountain`} x1='0' y1='0' x2='0' y2='1'>
          <stop stopColor='#242424' stopOpacity='.24' />
          <stop offset='1' stopColor='#242424' stopOpacity='0' />
        </linearGradient>
        <linearGradient id={`${id}-mist`} x1='0' y1='0' x2='1' y2='0'>
          <stop stopColor='#fafafa' stopOpacity='0' />
          <stop offset='.5' stopColor='#fafafa' stopOpacity='.94' />
          <stop offset='1' stopColor='#fafafa' stopOpacity='0' />
        </linearGradient>
      </defs>
      <g className='ops-ink-landscape' fill={`url(#${id}-mountain)`}>
        <path d='M15 419L64 361L101 381L148 312L175 347L207 287L247 333L276 309L305 362L335 321L370 362L409 283L440 328L473 301L527 370L584 413V505H15Z' />
        <path
          d='M14 441L69 405L111 421L158 370L200 405L242 381L290 433L331 395L372 429L420 368L473 398L527 384L587 440V505H14Z'
          opacity='.65'
        />
      </g>
      <g className='ops-ink-brush' fill='none' strokeLinecap='round'>
        <path d='M121 111C204 42 351 34 449 105C547 177 565 305 514 405C464 509 355 555 240 533C135 512 63 427 55 326C45 232 72 159 121 111' />
        <path d='M159 78C242 35 353 47 429 97M515 183C561 301 516 444 409 507M126 469C47 393 32 269 87 174' />
        <path d='M155 102C239 58 365 65 445 145M500 339C479 427 406 486 326 498M105 345C89 292 101 223 131 178' />
      </g>
      <g className='ops-ink-ripple' fill='none'>
        <ellipse cx='300' cy='456' rx='181' ry='19' />
        <ellipse cx='300' cy='456' rx='225' ry='30' />
      </g>
      <path
        className='ops-ink-mist'
        d='M30 382Q170 351 300 383T570 382V435Q410 401 300 425T30 423Z'
        fill={`url(#${id}-mist)`}
      />
      <g
        className='ops-ink-birds'
        fill='none'
        stroke='#3d3d3d'
        strokeLinecap='round'
      >
        <path d='M424 149Q433 143 441 149Q448 140 456 141M451 165Q458 161 464 166Q469 159 477 161' />
      </g>
    </svg>
  )
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
      aria-labelledby={labelId}
      aria-describedby={noteId}
    >
      <div className='ops-holo-stage'>
        <InkLandscapeArtwork />
        <div className='ops-holo-readout'>
          <p className='ops-holo-eyebrow'>{t(status)}</p>
          <output className='ops-holo-value' aria-labelledby={labelId}>
            {data ? format(data.primary) : '—'}
          </output>
          <h2 id={labelId} className='ops-holo-label'>
            {t(data?.primary.labelKey ?? 'No successful sample yet')}
          </h2>
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
      <p id={noteId} className='ops-holo-note'>
        {t(data?.noteKey ?? 'No chart observations')}
      </p>
      {data?.fundingNotCollected && (
        <p className='ops-holo-note ops-holo-note--funding'>
          {t(
            'Paid-versus-gift funding allocations are not collected in this version. New consumption also remains unverified until a funding ledger is implemented.'
          )}
        </p>
      )}
    </section>
  )
}
