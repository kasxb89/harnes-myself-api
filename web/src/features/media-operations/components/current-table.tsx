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

export function MediaCurrentTable(props: { rows: MediaOperationModel[] }) {
  const { t } = useTranslation()
  const format = useMediaFormat()
  const numericClass = 'text-right tabular-nums'
  const states = [
    { key: 'not_start', label: t('Recorded not started') },
    { key: 'submitted', label: t('Submitted') },
    { key: 'queued', label: t('Upstream-reported queued') },
    { key: 'in_progress', label: t('In progress') },
    { key: 'unknown', label: t('Recorded unknown states') },
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
    {
      id: 'kind',
      header: t('Media type'),
      cell: (row) => {
        if (row.media_kind === 'image') return t('Image')
        if (row.media_kind === 'video') return t('Video')
        return t('Unknown')
      },
    },
    ...states.map((state) => ({
      id: state.key,
      header: state.label,
      className: numericClass,
      cellClassName: numericClass,
      cell: (row: MediaOperationModel) => format.count(row.tasks[state.key]),
    })),
    {
      id: 'capacity',
      header: t('Configured concurrency'),
      cell: () => t('Unknown'),
    },
  ]
  if (!props.rows.length) {
    return (
      <EmptyState
        title={t('No recorded media models')}
        description={t('Choose another model or media type.')}
      />
    )
  }
  return (
    <StaticDataTable
      columns={columns}
      data={props.rows}
      getRowKey={(row) => `${row.media_kind}:${row.model}`}
      tableProps={{ 'aria-label': t('Current media task records') }}
    />
  )
}
