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

import { DataTablePage, useDataTable } from '@/components/data-table'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { formatCatalogCny } from './price-format'
import type { YcapiModel } from './types'

type Row = NonNullable<YcapiModel['priceRows']>[number] & {
  id: string
  name: string
  kind: YcapiModel['kind']
}

export function PricebookTable(props: { models: YcapiModel[] }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [search, setSearch] = useState('')
  const [kind, setKind] = useState('all')
  const [pagination, setPagination] = useState({ pageIndex: 0, pageSize: 20 })
  const rows = useMemo(
    () =>
      props.models
        .flatMap((model) =>
          (model.priceRows || []).map((row, index) => ({
            ...row,
            id: `${model.id}:${index}`,
            name: model.name,
            kind: model.kind,
          }))
        )
        .filter(
          (row) =>
            (kind === 'all' || row.kind === kind) &&
            `${row.name} ${row.modelId} ${JSON.stringify(row.params)}`
              .toLowerCase()
              .includes(search.trim().toLowerCase())
        ),
    [props.models, kind, search]
  )
  const kindLabels = {
    text: t('Text'),
    image: t('Image'),
    video: t('Video'),
    audio: t('Audio'),
  }
  const columns: ColumnDef<Row, unknown>[] = [
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
      accessorKey: 'kind',
      header: t('Type'),
      cell: ({ row }) => kindLabels[row.original.kind],
    },
    {
      id: 'spec',
      header: t('Parameters'),
      cell: ({ row }) => (
        <div className='min-w-40 text-xs'>
          {row.original.direction
            ? t(row.original.direction === 'input' ? 'Input' : 'Output')
            : Object.entries(row.original.params)
                .map(([k, v]) => `${k}: ${String(v)}`)
                .join(' / ')}
        </div>
      ),
    },
    {
      id: 'unit',
      header: t('Unit'),
      cell: ({ row }) => {
        if (row.original.kind === 'video') return t('CNY / second')
        if (row.original.kind === 'image') return t('CNY / image')
        return t('CNY / 1M tokens')
      },
    },
    {
      accessorKey: 'procurementCny',
      header: t('Procurement price'),
      cell: ({ row }) => formatCatalogCny(row.original.procurementCny, locale),
    },
    {
      accessorKey: 'retailCny',
      header: t('Retail price'),
      cell: ({ row }) => (
        <strong>{formatCatalogCny(row.original.retailCny, locale)}</strong>
      ),
    },
    {
      accessorKey: 'officialCny',
      header: t('Official benchmark'),
      cell: ({ row }) => (
        <a
          className='underline underline-offset-4'
          href={row.original.source}
          target='_blank'
          rel='noreferrer'
        >
          {formatCatalogCny(row.original.officialCny, locale)}
        </a>
      ),
    },
    {
      id: 'discount',
      header: t('Retail discount'),
      cell: ({ row }) =>
        t('{{value}} / 10 of official price', {
          value: formatNumber(row.original.retailZhe, locale),
        }),
    },
    {
      id: 'total',
      header: t('Native clip total'),
      cell: ({ row }) =>
        row.original.kind === 'video' &&
        row.original.retailTotalCny !== undefined
          ? formatCatalogCny(row.original.retailTotalCny, locale)
          : '—',
    },
  ]
  const { table } = useDataTable({
    data: rows,
    columns,
    enableRowSelection: false,
    enableSorting: false,
    pagination,
    onPaginationChange: setPagination,
    getRowId: (row) => row.id,
  })
  return (
    <section aria-label={t('Approved model price list')} className='space-y-3'>
      <DataTablePage
        table={table}
        columns={columns}
        fixedHeight={false}
        paginationInFooter={false}
        toolbar={
          <div className='flex flex-wrap items-end gap-3'>
            <label className='min-w-48 flex-1 space-y-1 text-sm'>
              <span>{t('Search prices')}</span>
              <Input
                value={search}
                maxLength={256}
                onChange={(event) => {
                  setSearch(event.target.value)
                  setPagination((p) => ({ ...p, pageIndex: 0 }))
                }}
              />
            </label>
            <label className='space-y-1 text-sm'>
              <span>{t('Type')}</span>
              <NativeSelect
                aria-label={t('Price type')}
                value={kind}
                onChange={(event) => {
                  setKind(event.target.value)
                  setPagination((p) => ({ ...p, pageIndex: 0 }))
                }}
              >
                <NativeSelectOption value='all'>{t('All')}</NativeSelectOption>
                {Object.entries(kindLabels).map(([key, label]) => (
                  <NativeSelectOption key={key} value={key}>
                    {label}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </label>
          </div>
        }
        emptyTitle={t('No matching prices')}
        tableClassName='[&_table]:min-w-[1060px]'
      />
    </section>
  )
}
