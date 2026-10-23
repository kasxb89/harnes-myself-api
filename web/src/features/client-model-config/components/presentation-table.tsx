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
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'

import type { ClientModelOption, ClientModelPresentationEntry } from '../types'

type PresentationTableProps = {
  models: ClientModelOption[]
  entries: Record<string, ClientModelPresentationEntry>
  search: string
  disabled: boolean
  onEdit: (id: string) => void
}
type PresentationRow = { id: string; available: boolean; displayName: string }

export function PresentationTable(props: PresentationTableProps) {
  const { t } = useTranslation()
  const catalog = new Map(props.models.map((model) => [model.id, model]))
  const ids = [...new Set([...catalog.keys(), ...Object.keys(props.entries)])]
  const search = props.search.trim().toLowerCase()
  const rows: PresentationRow[] = ids
    .map((id) => ({
      id,
      available: catalog.has(id),
      displayName: catalog.get(id)?.display_name ?? id,
    }))
    .filter((row) =>
      `${row.id} ${row.displayName}`.toLowerCase().includes(search)
    )
  const columns: StaticDataTableColumn<PresentationRow>[] = [
    {
      id: 'model',
      header: t('Model'),
      cellClassName: 'whitespace-normal',
      cell: (row) => (
        <div className='flex min-w-0 flex-col gap-1'>
          <span className='break-all'>{row.id}</span>
          {!row.available && (
            <Badge
              variant='outline'
              className='h-auto max-w-full whitespace-normal'
            >
              {t('Unavailable')}
            </Badge>
          )}
        </div>
      ),
    },
    {
      id: 'presentation',
      header: t('Description and badge'),
      cellClassName: 'whitespace-normal',
      cell: (row) => (
        <div className='flex min-w-0 flex-col gap-2'>
          {(['description', 'badge'] as const).map((field) => {
            const text = props.entries[row.id]?.[field]
            let display = t('Follow client default')
            if (text === '') display = t('Do not display')
            else if (text !== undefined) display = text
            return (
              <div key={field} className='min-w-0'>
                <span className='text-muted-foreground text-xs'>
                  {field === 'description'
                    ? t('Model description')
                    : t('Model badge')}
                </span>
                <p className='line-clamp-3 break-words whitespace-pre-wrap'>
                  {display}
                </p>
              </div>
            )
          })}
        </div>
      ),
    },
    {
      id: 'actions',
      header: t('Actions'),
      className: 'w-20',
      cell: (row) => (
        <Button
          type='button'
          variant='outline'
          size='sm'
          disabled={props.disabled}
          onClick={() => props.onEdit(row.id)}
          aria-label={t('Edit presentation for {{model}}', { model: row.id })}
        >
          {t('Edit')}
        </Button>
      ),
    },
  ]
  return (
    <StaticDataTable
      columns={columns}
      data={rows}
      getRowKey={(row) => row.id}
      tableProps={{ 'aria-label': t('Model descriptions and badges') }}
      tableClassName='table-fixed'
      className='max-h-[60vh] overflow-auto'
      emptyContent={search ? t('No matching models') : t('No available models')}
    />
  )
}
