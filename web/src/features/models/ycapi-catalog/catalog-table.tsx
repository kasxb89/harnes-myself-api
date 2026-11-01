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
import type { ColumnDef } from '@tanstack/react-table'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { DataTablePage, useDataTable } from '@/components/data-table'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { toIntlLocale } from '@/i18n/languages'

import { YcapiModelDetails } from './model-details'
import { formatCatalogCny } from './price-format'
import {
  PricebookStateBadge,
  type ModelPricebookState,
} from './pricebook-state'
import type { YcapiModel } from './types'

export function CatalogTable(props: {
  models: YcapiModel[]
  states?: Record<string, ModelPricebookState>
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const kindLabels = {
    image: t('Image'),
    video: t('Video'),
    text: t('Text'),
    audio: t('Audio'),
  }
  function priceSummary(
    model: YcapiModel,
    field: 'procurementCny' | 'retailCny'
  ) {
    const rows = model.priceRows || []
    if (!rows.length) return t('Unknown')
    if (model.kind === 'text' || model.kind === 'audio') {
      return (
        <div className='space-y-1 text-xs'>
          {rows.map((row) => (
            <div key={row.direction}>
              {row.direction === 'input' ? t('Input') : t('Output')}:{' '}
              {formatCatalogCny(row[field], locale)}
            </div>
          ))}
          <div className='text-muted-foreground'>{t('CNY / 1M tokens')}</div>
        </div>
      )
    }
    const values = rows.map((row) => row[field])
    const min = Math.min(...values),
      max = Math.max(...values)
    return (
      <div className='text-xs'>
        {formatCatalogCny(min, locale)}
        {max > min && <> – {formatCatalogCny(max, locale)}</>}
        <div className='text-muted-foreground'>
          {model.kind === 'image' ? t('CNY / image') : t('CNY / second')}
        </div>
      </div>
    )
  }
  const [filter, setFilter] = useState('')
  const [kind, setKind] = useState('all')
  const [selected, setSelected] = useState<YcapiModel | null>(null)
  const [pagination, setPagination] = useState({ pageIndex: 0, pageSize: 10 })
  const visible = useMemo(() => {
    const search = filter.trim().toLocaleLowerCase()
    return props.models.filter(
      (model) =>
        (kind === 'all' || model.kind === kind) &&
        (!search ||
          [model.name, model.id, model.upstreamModelId, model.provider].some(
            (value) => value.toLocaleLowerCase().includes(search)
          ))
    )
  }, [props.models, filter, kind])
  const columns: ColumnDef<YcapiModel, unknown>[] = [
    {
      accessorKey: 'name',
      header: t('Name'),
      meta: { mobileTitle: true },
      cell: ({ row }) => (
        <span className='block max-w-64 truncate' title={row.original.name}>
          {row.original.name}
        </span>
      ),
    },
    {
      id: 'activation',
      header: t('Status'),
      cell: ({ row }) => (
        <PricebookStateBadge state={props.states?.[row.original.id]} />
      ),
    },
    {
      accessorKey: 'kind',
      header: t('Type'),
      cell: ({ row }) => kindLabels[row.original.kind],
    },
    {
      accessorKey: 'id',
      header: t('Client model ID'),
      cell: ({ row }) => (
        <div className='flex min-w-0 items-center gap-1'>
          <code className='max-w-56 truncate' title={row.original.id}>
            {row.original.id}
          </code>
          <CopyButton
            value={row.original.id}
            aria-label={t('Copy client model ID')}
            className='size-7'
          />
        </div>
      ),
    },
    {
      accessorKey: 'upstreamModelId',
      header: t('Upstream model ID'),
      cell: ({ row }) => (
        <code
          className='block max-w-56 truncate'
          title={row.original.upstreamModelId}
        >
          {row.original.upstreamModelId}
        </code>
      ),
    },
    { accessorKey: 'provider', header: t('Provider') },
    {
      id: 'price',
      header: t('Current procurement cost'),
      cell: ({ row }) => priceSummary(row.original, 'procurementCny'),
    },
    {
      id: 'retail',
      header: t('Retail price'),
      cell: ({ row }) => priceSummary(row.original, 'retailCny'),
    },
    {
      id: 'actions',
      header: t('Actions'),
      cell: ({ row }) => (
        <Button
          variant='outline'
          size='sm'
          aria-label={t('View parameters: {{name}}', {
            name: row.original.name,
          })}
          onClick={() => setSelected(row.original)}
        >
          {t('View parameters')}
        </Button>
      ),
    },
  ]
  const { table } = useDataTable({
    data: visible,
    columns,
    enableRowSelection: false,
    enableSorting: false,
    pagination,
    onPaginationChange: setPagination,
    getRowId: (model) => model.id,
  })
  return (
    <>
      <DataTablePage
        table={table}
        columns={columns}
        fixedHeight={false}
        paginationInFooter={false}
        toolbar={
          <div className='flex flex-wrap items-end gap-2'>
            <label className='min-w-48 flex-1 space-y-1 text-sm'>
              <span>{t('Search YCAPI models')}</span>
              <Input
                value={filter}
                maxLength={256}
                onChange={(event) => {
                  setFilter(event.target.value)
                  setPagination((value) => ({ ...value, pageIndex: 0 }))
                }}
              />
            </label>
            <label className='space-y-1 text-sm'>
              <span>{t('Type')}</span>
              <NativeSelect
                aria-label={t('Type')}
                value={kind}
                onChange={(event) => {
                  setKind(event.target.value)
                  setPagination((value) => ({ ...value, pageIndex: 0 }))
                }}
              >
                <NativeSelectOption value='all'>{t('All')}</NativeSelectOption>
                <NativeSelectOption value='image'>
                  {t('Image')}
                </NativeSelectOption>
                <NativeSelectOption value='video'>
                  {t('Video')}
                </NativeSelectOption>
                <NativeSelectOption value='text'>
                  {t('Text')}
                </NativeSelectOption>
                <NativeSelectOption value='audio'>
                  {t('Audio')}
                </NativeSelectOption>
              </NativeSelect>
            </label>
          </div>
        }
        emptyTitle={t('No matching YCAPI models')}
        tableClassName='[&_table]:min-w-[760px]'
      />
      {selected && (
        <YcapiModelDetails model={selected} onClose={() => setSelected(null)} />
      )}
    </>
  )
}
