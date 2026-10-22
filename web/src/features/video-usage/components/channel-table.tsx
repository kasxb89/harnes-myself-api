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
import { EmptyState } from '@/components/empty-state'

import {
  channelOptionLabel,
  useVideoFormat,
  videoProviderName,
} from '../lib/format'
import type { VideoUsageResult } from '../schema'

type ChannelRow = VideoUsageResult['channel_breakdown'][number]
export function VideoChannelTable(props: { rows: ChannelRow[] }) {
  const { t } = useTranslation()
  const format = useVideoFormat()
  const columns: StaticDataTableColumn<ChannelRow>[] = [
    {
      id: 'channel',
      header: t('Channel'),
      className: 'min-w-48',
      cell: (row) => (
        <span className='break-all'>
          {channelOptionLabel(row.channel, t('Unknown channel'))}
        </span>
      ),
    },
    {
      id: 'provider',
      header: t('Provider (current settings)'),
      cell: (row) => videoProviderName(row.channel.provider) || t('Unknown'),
    },
    {
      id: 'inflight',
      header: t('Recorded in-flight tasks'),
      cell: (row) => format.count(row.current.inflight),
    },
    {
      id: 'queued',
      header: t('Upstream reports queued'),
      cell: (row) => format.count(row.current.queued),
    },
    {
      id: 'unknown',
      header: t('Recorded unknown states'),
      cell: (row) => format.count(row.current.unknown),
    },
    {
      id: 'tasks',
      header: t('Submitted in the history period'),
      cell: (row) => format.count(row.history.task_count),
    },
    {
      id: 'peak',
      header: t('Recorded interval peak'),
      cell: (row) => (
        <div className='space-y-1 text-xs'>
          <div>
            {row.peak.value === null
              ? t('Unknown')
              : format.count(row.peak.value)}
          </div>
          <div className='text-muted-foreground'>
            {t('Valid / excluded intervals')}:{' '}
            {format.count(row.peak.interval_samples)} /{' '}
            {format.count(row.peak.excluded_intervals)}
          </div>
        </div>
      ),
    },
    {
      id: 'p95',
      header: t('Terminal turnaround P95'),
      cell: (row) => format.duration(row.history.turnaround.p95_seconds),
    },
  ]
  return (
    <div className='space-y-3'>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Providers reflect current channel settings, not historical supplier identity or independent accounts.'
        )}
      </p>
      {props.rows.length === 0 ? (
        <EmptyState title={t('No recorded video tasks')} />
      ) : (
        <StaticDataTable
          columns={columns}
          data={props.rows}
          getRowKey={(row) => row.channel.id}
          tableProps={{ 'aria-label': t('Video usage by channel') }}
        />
      )}
    </div>
  )
}
