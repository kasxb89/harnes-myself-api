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
import type { PaginationState, Updater } from '@tanstack/react-table'
import { useTranslation } from 'react-i18next'

import { DataTablePage, useDataTable } from '@/components/data-table'
import { ComboboxInput } from '@/components/ui/combobox-input'
import { Field, FieldLabel } from '@/components/ui/field'

import type { VideoUsageResult } from '../schema'
import { useVideoTaskColumns } from './task-columns'

export function VideoTaskTable(props: {
  tasks: VideoUsageResult['tasks']
  loading: boolean
  onScopeChange: (scope: 'history' | 'current') => void
  onPageChange: (value: Updater<PaginationState>) => void
}) {
  const { t } = useTranslation()
  const columns = useVideoTaskColumns()
  const { table } = useDataTable({
    data: props.tasks.items,
    columns,
    totalCount: props.tasks.total,
    pagination: {
      pageIndex: props.tasks.page - 1,
      pageSize: props.tasks.page_size,
    },
    onPaginationChange: props.onPageChange,
    manualPagination: true,
    manualFiltering: true,
    enableSorting: false,
    enableRowSelection: false,
    columnVisibilityStorageKey: false,
    columnSizingStorageKey: false,
  })
  return (
    <section aria-label={t('Video task details')} className='min-w-0 space-y-3'>
      <Field className='max-w-72'>
        <FieldLabel htmlFor='video-task-scope'>
          {t('Task list scope')}
        </FieldLabel>
        <ComboboxInput
          id='video-task-scope'
          aria-label={t('Task list scope')}
          allowCustomValue={false}
          openOnFocus={false}
          disabled={props.loading}
          value={props.tasks.scope}
          options={[
            { value: 'history', label: t('Submitted in the history period') },
            { value: 'current', label: t('Current nonterminal records') },
          ]}
          onValueChange={(scope) => {
            if (scope === 'history' || scope === 'current') {
              props.onScopeChange(scope)
            }
          }}
        />
      </Field>
      <DataTablePage
        table={table}
        columns={columns}
        isFetching={props.loading}
        emptyTitle={t('No recorded video tasks')}
        toolbarProps={null}
        fixedHeight={false}
        paginationInFooter={false}
        applyHeaderSize
        className='min-w-0'
        getColumnClassName={() => 'align-top py-3'}
      />
    </section>
  )
}
