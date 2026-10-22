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

import { useMediaFormat } from '../lib/format'
import type { MediaOperationModel } from '../schema'

export function MediaHistoryTable(props: { rows: MediaOperationModel[] }) {
  const { t } = useTranslation()
  const format = useMediaFormat()
  const numericClass = 'text-right tabular-nums'
  const metrics = [
    { key: 'accepted_tasks', label: t('Recorded accepted tasks') },
    { key: 'error_requests', label: t('Distinct error requests') },
    { key: 'error_events', label: t('Error record events') },
    { key: 'observed_policy_attempts', label: t('Recorded policy attempts') },
    { key: 'client_report_events', label: t('Client report events') },
  ] as const
  const columns: StaticDataTableColumn<MediaOperationModel>[] = [
    {
      id: 'model',
      header: t('Model'),
      className: 'min-w-52',
      cell: (row) => (
        <span className='font-mono text-xs break-all'>
          {row.model || t('Unknown model')}
        </span>
      ),
    },
    ...metrics.map((metric) => ({
      id: metric.key,
      header: metric.label,
      className: numericClass,
      cellClassName: numericClass,
      cell: (row: MediaOperationModel) => format.count(row.history[metric.key]),
    })),
  ]
  if (!props.rows.length) {
    return (
      <EmptyState
        title={t('No media observations in this period')}
        description={t('Choose another period or model.')}
      />
    )
  }
  return (
    <StaticDataTable
      columns={columns}
      data={props.rows}
      getRowKey={(row) => `${row.media_kind}:${row.model}`}
      tableProps={{ 'aria-label': t('Media history by model') }}
    />
  )
}
