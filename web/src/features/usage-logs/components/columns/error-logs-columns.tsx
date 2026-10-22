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
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import dayjs from '@/lib/dayjs'

import type { UsageLog } from '../../data/schema'
import { getErrorHttpStatus } from '../../lib/error-diagnostics'
import { parseLogOther } from '../../lib/format'

/** Keep user, model, channel and the full details dialog shared with common logs. */
export function useErrorLogsColumns(
  commonColumns: ColumnDef<UsageLog>[]
): ColumnDef<UsageLog>[] {
  const { t } = useTranslation()
  return useMemo(() => {
    const columns = commonColumns
      .filter((column) => {
        const id =
          column.id ??
          ('accessorKey' in column ? column.accessorKey : undefined)
        return [
          'created_at',
          'user',
          'model_name',
          'channel',
          'content',
        ].includes(String(id))
      })
      .map((column) => {
        if (!('accessorKey' in column) || column.accessorKey !== 'created_at') {
          return column
        }
        return {
          ...column,
          size: 240,
          cell: ({ row }: { row: { original: UsageLog } }) => (
            <time
              dateTime={new Date(row.original.created_at * 1000).toISOString()}
              className='font-mono text-xs whitespace-nowrap tabular-nums'
            >
              {dayjs
                .unix(row.original.created_at)
                .format('YYYY-MM-DD HH:mm:ss Z')}
            </time>
          ),
        }
      })
    columns.push(
      {
        id: 'error_status',
        header: t('Recorded status'),
        cell: ({ row }) => {
          const other = parseLogOther(row.original.other)
          const status = getErrorHttpStatus(other)
          return (
            <div className='space-y-1 font-mono text-xs'>
              <span>
                {status.recorded ?? '—'}
                {status.mapped && ` (${t('Task mapping')})`}
              </span>
              {status.upstream !== undefined && (
                <div>
                  {t('Upstream HTTP Status')}: {status.upstream}
                </div>
              )}
            </div>
          )
        },
        size: 100,
      },
      {
        accessorKey: 'request_id',
        header: t('Request ID'),
        cell: ({ row }) =>
          row.original.request_id ? (
            <div className='flex min-w-0 items-center gap-1'>
              <span className='max-w-44 truncate font-mono text-xs'>
                {row.original.request_id}
              </span>
              <CopyButton value={row.original.request_id} />
            </div>
          ) : (
            <span>—</span>
          ),
        size: 220,
      }
    )
    return columns
  }, [commonColumns, t])
}
