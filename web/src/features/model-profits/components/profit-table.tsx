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
import { Badge } from '@/components/ui/badge'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { formatProfitCny, formatProfitMargin } from '../lib/format'
import type { ModelProfitRow } from '../types'

export function ProfitTable(props: { rows: ModelProfitRow[] }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const money = (value: number | null) =>
    value === null ? t('Unknown') : formatProfitCny(value, locale)
  const numericClass = 'text-right tabular-nums'
  const columns: StaticDataTableColumn<ModelProfitRow>[] = [
    {
      id: 'model',
      header: t('Model'),
      className: 'min-w-52',
      cell: (row) => (
        <span className='font-mono text-xs break-all'>
          {row.model_name || t('Unknown model')}
        </span>
      ),
    },
    {
      id: 'requests',
      header: t('Requests'),
      className: numericClass,
      cellClassName: numericClass,
      cell: (row) => formatNumber(row.requests, locale),
    },
    {
      id: 'revenue',
      header: t('Billed revenue'),
      className: numericClass,
      cellClassName: numericClass,
      cell: (row) => money(row.revenue_cny),
    },
    {
      id: 'known-revenue',
      header: t('Revenue with known costs'),
      className: numericClass,
      cellClassName: numericClass,
      cell: (row) => money(row.known_cost_revenue_cny),
    },
    {
      id: 'cost',
      header: t('Configured upstream cost (estimate)'),
      className: numericClass,
      cellClassName: numericClass,
      cell: (row) => money(row.upstream_cost_cny),
    },
    {
      id: 'profit',
      header: t('Billed gross profit'),
      className: numericClass,
      cellClassName: numericClass,
      cell: (row) => (
        <span
          className={
            row.gross_profit_cny !== null && row.gross_profit_cny < 0
              ? 'text-destructive'
              : undefined
          }
        >
          {money(row.gross_profit_cny)}
        </span>
      ),
    },
    {
      id: 'margin',
      header: t('Gross margin'),
      className: numericClass,
      cellClassName: numericClass,
      cell: (row) => formatProfitMargin(row.profit_margin, locale),
    },
    {
      id: 'coverage',
      header: t('Billing cost coverage'),
      className: numericClass,
      cellClassName: numericClass,
      cell: (row) => (
        <div className='flex items-center justify-end gap-2'>
          <span>{formatNumber(row.cost_coverage * 100, locale)}%</span>
          {row.unknown_cost_events > 0 && (
            <Badge variant='outline'>{t('Partial')}</Badge>
          )}
        </div>
      ),
    },
    {
      id: 'unknown-events',
      header: t('Unknown cost events'),
      className: numericClass,
      cellClassName: numericClass,
      cell: (row) => formatNumber(row.unknown_cost_events, locale),
    },
    {
      id: 'unknown',
      header: t('Unknown cost requests'),
      className: numericClass,
      cellClassName: numericClass,
      cell: (row) => formatNumber(row.unknown_cost_requests, locale),
    },
    {
      id: 'pending',
      header: t('Pending settlement'),
      className: numericClass,
      cellClassName: numericClass,
      cell: (row) => formatNumber(row.pending_requests, locale),
    },
    {
      id: 'refund',
      header: t('Refunded amount'),
      className: numericClass,
      cellClassName: numericClass,
      cell: (row) => money(row.refund_cny),
    },
  ]
  if (props.rows.length === 0) {
    return (
      <EmptyState
        title={t('No model consumption in this period')}
        description={t('Choose another period or model.')}
      />
    )
  }
  return (
    <StaticDataTable
      columns={columns}
      data={props.rows}
      getRowKey={(row) => row.model_name}
      tableProps={{ 'aria-label': t('Profit by model') }}
    />
  )
}
