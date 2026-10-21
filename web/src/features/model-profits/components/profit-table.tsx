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
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { formatProfitCny } from '../lib/format'
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
