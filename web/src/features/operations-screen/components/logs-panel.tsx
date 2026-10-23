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
import { Activity, AlertTriangle, FileText, Hash } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { StatCard } from '@/features/dashboard/components/ui/stat-card'
import type { UsageLog } from '@/features/usage-logs/data/schema'
import { getErrorDiagnosticCopy } from '@/features/usage-logs/lib/error-diagnostic-copy'
import {
  getErrorHttpStatus,
  parseErrorDiagnostics,
} from '@/features/usage-logs/lib/error-diagnostics'
import { parseLogOther } from '@/features/usage-logs/lib/format'
import { ActivityTimestamp } from '@/features/user-activity/display'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import type { OperationsSnapshot } from '../api'

export function OperationsLogsPanel(props: {
  snapshot: Extract<OperationsSnapshot, { tab: 'usage' | 'errors' }>
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const errorView = props.snapshot.tab === 'errors'
  const stats =
    props.snapshot.tab === 'usage' ? props.snapshot.stats : undefined
  const number = (value: number) => formatNumber(value, locale)
  const columns: StaticDataTableColumn<UsageLog>[] = [
    {
      id: 'time',
      header: t('Time'),
      cell: (row) => <ActivityTimestamp value={row.created_at} />,
    },
    {
      id: 'user',
      header: t('User'),
      cell: (row) => (
        <span className='block max-w-40 break-words'>
          {row.username || t('Unknown')}
        </span>
      ),
    },
    {
      id: 'model',
      header: t('Model'),
      className: 'min-w-44',
      cell: (row) => (
        <span className='block max-w-60 font-mono text-xs break-all'>
          {row.model_name || t('Unknown model')}
        </span>
      ),
    },
    {
      id: 'channel',
      header: t('Channel'),
      cell: (row) => (
        <span className='block max-w-40 break-words'>
          {row.channel_name || number(row.channel)}
        </span>
      ),
    },
    {
      id: 'request',
      header: t('Request ID'),
      cell: (row) =>
        row.request_id ? (
          <div className='flex max-w-60 items-start gap-1'>
            <span className='min-w-0 font-mono text-xs break-all'>
              {row.request_id}
            </span>
            <CopyButton value={row.request_id} />
          </div>
        ) : (
          t('Unknown')
        ),
    },
  ]
  if (errorView) {
    columns.push(
      {
        id: 'cause',
        header: t('Error reason'),
        className: 'min-w-52',
        cell: (row) => {
          const diagnostic = parseErrorDiagnostics(
            row.content,
            parseLogOther(row.other)
          )
          const copy = getErrorDiagnosticCopy(
            diagnostic?.reason ?? 'unknown',
            t
          )
          return (
            <div className='max-w-80 space-y-1 break-words whitespace-normal'>
              <p className='font-medium'>{copy.title}</p>
              <p className='text-muted-foreground text-xs'>
                {copy.description}
              </p>
            </div>
          )
        },
      },
      {
        id: 'status',
        header: t('Recorded status'),
        cell: (row) => {
          const status = getErrorHttpStatus(parseLogOther(row.other))
          return (
            <div className='space-y-1 text-xs'>
              <p>
                {status.recorded ?? t('Unknown')}
                {status.mapped && ` (${t('Task mapping')})`}
              </p>
              {status.upstream !== undefined && (
                <p>
                  {t('Upstream HTTP Status')}: {number(status.upstream)}
                </p>
              )}
            </div>
          )
        },
      },
      {
        id: 'content',
        header: t('Original error'),
        className: 'min-w-60',
        cell: (row) => (
          <p className='max-w-96 text-xs break-all whitespace-pre-wrap'>
            {row.content || t('Unknown')}
          </p>
        ),
      }
    )
  } else {
    columns.push(
      {
        id: 'input',
        header: t('Input tokens'),
        className: 'text-right tabular-nums',
        cell: (row) => number(row.prompt_tokens),
      },
      {
        id: 'output',
        header: t('Output tokens'),
        className: 'text-right tabular-nums',
        cell: (row) => number(row.completion_tokens),
      },
      {
        id: 'duration',
        header: t('Recorded duration (seconds)'),
        className: 'text-right tabular-nums',
        cell: (row) => number(row.use_time),
      }
    )
  }
  return (
    <div className='min-w-0 space-y-5'>
      <section
        aria-label={t('Log observation summary')}
        className='grid grid-cols-2 gap-4 rounded-xl border p-4 xl:grid-cols-3'
      >
        <StatCard
          title={
            errorView
              ? t('API error records in range')
              : t('Consumption records in range')
          }
          value={number(props.snapshot.logs.total)}
          description={t('Matching log rows, not distinct requests')}
          icon={errorView ? AlertTriangle : FileText}
          compactMobile
        />
        {stats && (
          <>
            <StatCard
              title={t('Consumption records in the last 60 seconds')}
              value={number(stats.rpm)}
              description={t(
                'Server rolling 60-second sample, independent of the selected history window'
              )}
              icon={Activity}
              compactMobile
            />
            <StatCard
              title={t('Tokens recorded in the last 60 seconds')}
              value={number(stats.tpm)}
              description={t(
                'Server rolling 60-second sample, independent of the selected history window'
              )}
              icon={Hash}
              compactMobile
            />
          </>
        )}
      </section>
      <p className='text-muted-foreground text-sm'>
        {errorView
          ? t(
              'Gateway error records can include multiple upstream retry attempts for one request. Record counts are not failed-request counts or an API failure rate. Client-only reports are not included.'
            )
          : t(
              'Consumption records follow recording or settlement time. Missing or delayed records are not a complete live count of requests in flight.'
            )}
      </p>
      <section className='min-w-0 space-y-3'>
        <h2 className='font-medium'>
          {errorView
            ? t('Recent API error records')
            : t('Recent consumption records')}
        </h2>
        <p className='text-muted-foreground text-xs'>
          {t(
            'Showing the latest {{shown}} of {{total}} matching log records.',
            {
              shown: number(props.snapshot.logs.items.length),
              total: number(props.snapshot.logs.total),
            }
          )}
        </p>
        <StaticDataTable
          columns={columns}
          data={props.snapshot.logs.items}
          getRowKey={(row) => row.id}
          tableProps={{
            'aria-label': errorView
              ? t('Recent API error records')
              : t('Recent consumption records'),
          }}
          emptyContent={t('No log records in this period')}
        />
      </section>
    </div>
  )
}
