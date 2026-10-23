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
import {
  getCoreRowModel,
  getPaginationRowModel,
  useReactTable,
} from '@tanstack/react-table'
import type { UseFormReturn } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import {
  DataTablePagination,
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { Badge } from '@/components/ui/badge'
import {
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'

import type {
  MediaPool,
  MediaConcurrencyFormValues,
} from '../concurrency-schema'
import { useMediaFormat } from '../lib/format'

export function MediaConcurrencyTable(props: {
  pools: MediaPool[]
  form: UseFormReturn<MediaConcurrencyFormValues>
  disabled: boolean
}) {
  const { t } = useTranslation()
  const format = useMediaFormat()
  const table = useReactTable({
    data: props.pools,
    columns: [],
    getCoreRowModel: getCoreRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
    initialState: { pagination: { pageSize: 20 } },
  })
  const numeric = 'text-right tabular-nums'
  const columns: StaticDataTableColumn<MediaPool>[] = [
    {
      id: 'pool',
      header: t('Shared upstream account'),
      className: 'min-w-44',
      cell: (pool) => (
        <div className='space-y-1'>
          <p className='font-medium'>{pool.label}</p>
          <Badge variant='secondary'>
            {pool.media_kind === 'video' ? t('Video') : t('Image')}
          </Badge>
        </div>
      ),
    },
    {
      id: 'channels',
      header: t('Channels'),
      cell: (pool) => pool.channel_ids.map((id) => format.count(id)).join(', '),
    },
    {
      id: 'override',
      header: t('Pool override'),
      className: 'min-w-40',
      cell: (pool) => {
        const index = props.pools.indexOf(pool)
        return (
          <FormField
            control={props.form.control}
            name={`pools.${index}.limit`}
            render={({ field }) => (
              <FormItem>
                <FormLabel className='sr-only'>
                  {t('Override for {{pool}}', { pool: pool.label })}
                </FormLabel>
                <FormControl>
                  <Input
                    {...field}
                    inputMode='numeric'
                    placeholder={t('Inherit default')}
                    disabled={props.disabled}
                    className='w-28'
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
        )
      },
    },
    {
      id: 'configured',
      header: t('Configured policy limit'),
      className: numeric,
      cell: (pool) => format.count(pool.configured_limit),
    },
    {
      id: 'upstream',
      header: t('Known upstream limit'),
      className: numeric,
      cell: (pool) =>
        pool.known_upstream_limit === null
          ? t('Unknown')
          : format.count(pool.known_upstream_limit),
    },
    {
      id: 'effective',
      header: t('Effective admission limit'),
      className: numeric,
      cell: (pool) => format.count(pool.effective_limit),
    },
    {
      id: 'occupied',
      header: t('Occupied admission slots'),
      className: numeric,
      cell: (pool) => format.count(pool.occupied),
    },
    {
      id: 'unknown',
      header: t('UNKNOWN slots held'),
      className: numeric,
      cell: (pool) => format.count(pool.unknown),
    },
    {
      id: 'available',
      header: t('Available admission slots'),
      className: numeric,
      cell: (pool) =>
        pool.enabled && pool.reconciled
          ? format.count(pool.available_slots)
          : t('Unavailable'),
    },
    {
      id: 'state',
      header: t('Status'),
      cell: (pool) => {
        if (!pool.enabled) {
          return <Badge variant='secondary'>{t('Disabled')}</Badge>
        }
        if (!pool.reconciled) {
          return <Badge variant='outline'>{t('Reconciliation required')}</Badge>
        }
        return <Badge variant='secondary'>{t('Ready')}</Badge>
      },
    },
  ]
  if (!props.pools.length) {
    return (
      <EmptyState
        title={t('No registered media account pools')}
        description={t(
          'Pools appear when configured media keys are registered or first used.'
        )}
      />
    )
  }
  return (
    <div className='min-w-0 space-y-3'>
      <StaticDataTable
        columns={columns}
        data={table.getRowModel().rows.map((row) => row.original)}
        getRowKey={(row) => row.pool_id}
        tableProps={{ 'aria-label': t('Media account admission pools') }}
      />
      <DataTablePagination table={table} />
    </div>
  )
}
