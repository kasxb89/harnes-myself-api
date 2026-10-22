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

import { useVideoFormat } from '../lib/format'
import type { VideoUsageResult } from '../schema'
import { VideoDurationMetrics } from './duration-metrics'

type Model = VideoUsageResult['models'][number]
export function VideoHistoryTable(props: { models: Model[] }) {
  const { t } = useTranslation()
  const format = useVideoFormat()
  const metricClass = 'text-right tabular-nums'
  const metrics = [
    { key: 'task_count', label: t('Recorded tasks') },
    { key: 'success', label: t('Success') },
    { key: 'failure', label: t('Failed') },
    { key: 'unresolved', label: t('Unresolved') },
    { key: 'users', label: t('Distinct users') },
  ] as const
  const columns: StaticDataTableColumn<Model>[] = [
    {
      id: 'model',
      header: t('Model'),
      className: 'min-w-48',
      cell: (row) => (
        <span className='font-mono text-xs break-all'>
          {row.model || t('Unknown model')}
        </span>
      ),
    },
    ...metrics.map((metric) => ({
      id: metric.key,
      header: metric.label,
      className: metricClass,
      cellClassName: metricClass,
      cell: (row: Model) => format.count(row.history[metric.key]),
    })),
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
      id: 'turnaround',
      header: t('Terminal turnaround'),
      cell: (row) => <VideoDurationMetrics data={row.history.turnaround} />,
    },
    {
      id: 'running',
      header: t('Recorded running duration'),
      cell: (row) => <VideoDurationMetrics data={row.history.recorded_run} />,
    },
  ]
  if (!props.models.length) {
    return (
      <EmptyState
        title={t('No recorded video tasks')}
        description={t('Choose another period or model.')}
      />
    )
  }
  return (
    <StaticDataTable
      columns={columns}
      data={props.models}
      getRowKey={(row) => row.model}
      tableProps={{ 'aria-label': t('Video usage by model') }}
    />
  )
}
