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

import { toIntlLocale } from '@/i18n/languages'
import { cn } from '@/lib/utils'

import { formatProfitCny, verifiedCashAmount } from '../lib/format'
import type { ProfitTotals } from '../types'

export function ProfitSummary(props: { data: ProfitTotals }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const metrics = [
    {
      label: 'Cash revenue',
      amount: verifiedCashAmount(props.data, 'cash_revenue_cny'),
    },
    {
      label: 'Known model procurement cost (estimate)',
      amount: props.data.upstream_cost_cny,
    },
    {
      label: 'Cash profit',
      amount: verifiedCashAmount(props.data, 'cash_profit_cny'),
    },
  ]
  return (
    <section aria-label={t('Model profit summary')}>
      <dl className='grid gap-5 py-2 sm:grid-cols-3'>
        {metrics.map((metric) => {
          const confirmed =
            typeof metric.amount === 'number' && Number.isFinite(metric.amount)
          return (
            <div key={metric.label} className='min-w-0'>
              <dt className='text-muted-foreground text-sm'>
                {t(metric.label)}
              </dt>
              <dd
                className={cn(
                  'mt-2 text-2xl font-semibold break-words tabular-nums',
                  !confirmed && 'text-muted-foreground',
                  metric.label === 'Cash profit' &&
                    typeof metric.amount === 'number' &&
                    metric.amount < 0 &&
                    'text-destructive'
                )}
              >
                {confirmed
                  ? formatProfitCny(metric.amount, locale)
                  : t('Pending confirmation')}
              </dd>
            </div>
          )
        })}
      </dl>
    </section>
  )
}
