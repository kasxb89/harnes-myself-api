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

import { categoryLabels, stageLabels } from '../labels'
import { useMediaFormat } from '../lib/format'
import type { MediaOperationsResult } from '../schema'

type CategoryRow =
  MediaOperationsResult['history_summary']['categories'][number]
type StageRow = MediaOperationsResult['history_summary']['stages'][number]

export function MediaErrorBreakdown(props: {
  history: MediaOperationsResult['history_summary']
}) {
  const { t } = useTranslation()
  const format = useMediaFormat()
  const numericClass = 'text-right tabular-nums'
  const categoryColumns: StaticDataTableColumn<CategoryRow>[] = [
    {
      id: 'category',
      header: t('Observed error category'),
      className: 'min-w-48',
      cell: (row) => t(categoryLabels[row.category]),
    },
    {
      id: 'events',
      header: t('Error record events'),
      className: numericClass,
      cellClassName: numericClass,
      cell: (row) => format.count(row.events),
    },
    {
      id: 'requests',
      header: t('Distinct error requests'),
      className: numericClass,
      cellClassName: numericClass,
      cell: (row) => format.count(row.requests),
    },
  ]
  const stageColumns: StaticDataTableColumn<StageRow>[] = [
    {
      id: 'source',
      header: t('Observation source'),
      cell: (row) =>
        row.source === 'gateway' ? t('Gateway record') : t('Client report'),
    },
    {
      id: 'stage',
      header: t('Stage'),
      cell: (row) => t(stageLabels[row.stage]),
    },
    {
      id: 'events',
      header: t('Record events'),
      className: numericClass,
      cellClassName: numericClass,
      cell: (row) => format.count(row.events),
    },
    {
      id: 'requests',
      header: t('Requests with correlation IDs'),
      className: numericClass,
      cellClassName: numericClass,
      cell: (row) => format.count(row.requests),
    },
  ]
  return (
    <div className='grid min-w-0 gap-4 xl:grid-cols-2'>
      <section
        className='min-w-0 space-y-3'
        aria-label={t('Observed error categories')}
      >
        <h3 className='font-medium'>{t('Observed error categories')}</h3>
        {props.history.categories.length ? (
          <StaticDataTable
            columns={categoryColumns}
            data={props.history.categories}
            getRowKey={(row) => row.category}
            tableProps={{ 'aria-label': t('Observed error categories') }}
          />
        ) : (
          <EmptyState title={t('No recorded gateway errors')} />
        )}
      </section>
      <section
        className='min-w-0 space-y-3'
        aria-label={t('Observation stages')}
      >
        <h3 className='font-medium'>{t('Observation stages')}</h3>
        {props.history.stages.length ? (
          <StaticDataTable
            columns={stageColumns}
            data={props.history.stages}
            getRowKey={(row) => `${row.source}:${row.stage}`}
            tableProps={{ 'aria-label': t('Observation stages') }}
          />
        ) : (
          <EmptyState title={t('No recorded error stages')} />
        )}
      </section>
    </div>
  )
}
