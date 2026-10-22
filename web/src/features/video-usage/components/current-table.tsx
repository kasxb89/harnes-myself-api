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

type Model = VideoUsageResult['models'][number]
export function VideoCurrentTable(props: { models: Model[] }) {
  const { t } = useTranslation()
  const format = useVideoFormat()
  const metricClass = 'text-right tabular-nums'
  const metrics = [
    { key: 'inflight', label: t('Recorded in-flight tasks') },
    { key: 'queued', label: t('Upstream-reported queued') },
    { key: 'in_progress', label: t('In progress') },
    { key: 'unknown', label: t('Recorded unknown states') },
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
      cell: (row: Model) => format.count(row.current[metric.key]),
    })),
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
      tableProps={{ 'aria-label': t('Current video task records') }}
    />
  )
}
