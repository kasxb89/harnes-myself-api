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
import { useTranslation } from 'react-i18next'

import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import {
  ActivityDuration,
  ActivityStatus,
  ActivityTimestamp,
} from '@/features/user-activity/display'
import { ActivitySummary } from '@/features/user-activity/summary'
import type { ClientActivitySession } from '@/features/user-activity/types'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import type { OperationsSnapshot } from '../api'

export function OperationsActivityPanel(props: {
  snapshot: Extract<OperationsSnapshot, { tab: 'activity' }>
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const columns: StaticDataTableColumn<ClientActivitySession>[] = [
    {
      id: 'user',
      header: t('User'),
      cell: (row) => (
        <span className='block max-w-48 break-words'>{row.username}</span>
      ),
    },
    {
      id: 'status',
      header: t('Client status'),
      cell: (row) => <ActivityStatus status={row.status} />,
    },
    {
      id: 'platform',
      header: t('Platform'),
      cell: (row) => (
        <span className='block max-w-32 break-words'>
          {row.platform || t('Unknown')} · {row.app_version || t('Unknown')}
        </span>
      ),
    },
    {
      id: 'started',
      header: t('Session started'),
      cell: (row) => <ActivityTimestamp value={row.started_at} />,
    },
    {
      id: 'heartbeat',
      header: t('Last heartbeat'),
      cell: (row) => <ActivityTimestamp value={row.last_seen_at} />,
    },
    {
      id: 'duration',
      header: t('Online duration'),
      cell: (row) => <ActivityDuration seconds={row.online_seconds} />,
    },
    {
      id: 'calls',
      header: t('Model calls'),
      className: 'text-right tabular-nums',
      cell: (row) => formatNumber(row.model_calls, locale),
    },
    {
      id: 'errors',
      header: t('Model errors'),
      className: 'text-right tabular-nums',
      cell: (row) => formatNumber(row.model_errors, locale),
    },
  ]
  return (
    <div className='min-w-0 space-y-5'>
      <ActivitySummary
        summary={props.snapshot.summary}
        history
        loading={false}
        error={false}
      />
      <p className='text-muted-foreground text-xs'>
        {t('Server observation time')}:{' '}
        <ActivityTimestamp value={props.snapshot.summary.server_time} /> ·{' '}
        {t('Heartbeat timeout seconds', {
          seconds: props.snapshot.summary.online_timeout_seconds,
        })}
      </p>
      <section className='min-w-0 space-y-3'>
        <h2 className='font-medium'>{t('Currently online')}</h2>
        <p className='text-muted-foreground text-xs'>
          {t(
            'Showing {{shown}} of {{total}} current client processes. Presence is global and does not follow the history window.',
            {
              shown: formatNumber(props.snapshot.sessions.items.length, locale),
              total: formatNumber(props.snapshot.sessions.total, locale),
            }
          )}
        </p>
        <StaticDataTable
          data={props.snapshot.sessions.items}
          columns={columns}
          getRowKey={(row) => row.id}
          tableProps={{ 'aria-label': t('Current client processes') }}
          emptyContent={t('No client sessions')}
        />
      </section>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Clients without reporting, including older versions, are not treated as measured zero. An expired heartbeat does not prove that the client exited or an upstream task stopped.'
        )}
      </p>
    </div>
  )
}
