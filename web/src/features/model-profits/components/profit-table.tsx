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

import {
  formatProfitCny,
  verifiedCashAmount,
  verifiedFundingAmount,
  verifiedPeriodAverage,
} from '../lib/format'
import type { ModelProfitRow } from '../types'

export function ProfitTable(props: { rows: ModelProfitRow[] }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const money = (value: number | null | undefined) =>
    value == null || !Number.isFinite(value)
      ? t('Pending confirmation')
      : formatProfitCny(value, locale)
  const numericClass = 'text-right tabular-nums'
  const columns: StaticDataTableColumn<ModelProfitRow>[] = [
    {
      id: 'model',
      header: t('Model name'),
      className: 'min-w-52',
      cell: (row) => (
        <div className='space-y-2'>
          <span className='font-mono text-xs break-all'>
            {row.model_name || t('Unknown model')}
          </span>
          {row.funding_policy === 'fifo' && (
            <dl className='text-muted-foreground space-y-1 text-xs'>
              <div>
                <dt className='inline'>
                  {t('Confirmed paid consumption revenue')}:{' '}
                </dt>
                <dd className='inline tabular-nums'>
                  {money(
                    verifiedFundingAmount(row, 'confirmed_cash_revenue_cny')
                  )}
                </dd>
              </div>
              <div>
                <dt className='inline'>{t('Consumed paid face value')}: </dt>
                <dd className='inline tabular-nums'>
                  {money(
                    verifiedFundingAmount(row, 'confirmed_paid_consumed_cny')
                  )}
                </dd>
              </div>
              <div>
                <dt className='inline'>{t('Consumed gifted face value')}: </dt>
                <dd className='inline tabular-nums'>
                  {money(
                    verifiedFundingAmount(row, 'confirmed_gift_consumed_cny')
                  )}
                </dd>
              </div>
              <div>
                <dt className='inline'>
                  {t('Consumed face value awaiting attribution')}:{' '}
                </dt>
                <dd className='inline tabular-nums'>
                  {money(
                    verifiedFundingAmount(row, 'unknown_funding_consumed_cny')
                  )}
                </dd>
              </div>
            </dl>
          )}
        </div>
      ),
    },
    {
      id: 'requests',
      header: t('Usage count'),
      className: numericClass,
      cellClassName: numericClass,
      cell: (row) => formatNumber(row.requests, locale),
    },
    {
      id: 'customer-unit-price',
      header: t('Customer unit price'),
      className: numericClass,
      cellClassName: numericClass,
      cell: (row) => (
        <div>
          <div>
            {money(verifiedPeriodAverage(row, 'customer_average_price_cny'))}
          </div>
          <div className='text-muted-foreground text-xs'>
            <span>{t('Average per request')}</span> ·{' '}
            <span>{t('Balance charges')}</span>
          </div>
        </div>
      ),
    },
    {
      id: 'upstream-unit-cost',
      header: t('Procurement unit cost (estimate)'),
      className: numericClass,
      cellClassName: numericClass,
      cell: (row) => (
        <div>
          <div>
            {money(verifiedPeriodAverage(row, 'upstream_average_cost_cny'))}
          </div>
          <div className='text-muted-foreground text-xs'>
            {t('Average per request')}
          </div>
        </div>
      ),
    },
    {
      id: 'profit',
      header: t('Profit'),
      className: numericClass,
      cellClassName: numericClass,
      cell: (row) => money(verifiedCashAmount(row, 'cash_profit_cny')),
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
