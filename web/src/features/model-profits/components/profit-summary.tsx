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
import { CircleDollarSign, Gift, PiggyBank, Receipt } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { StatCard } from '@/features/dashboard/components/ui/stat-card'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { formatProfitCny, verifiedCashAmount } from '../lib/format'
import type { ProfitTotals } from '../types'

export function ProfitSummary(props: { data: ProfitTotals }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const data = props.data
  const cash = (
    field: 'cash_revenue_cny' | 'gift_consumed_cny' | 'cash_profit_cny'
  ) => {
    const amount = verifiedCashAmount(data, field)
    return amount === null
      ? t('Pending confirmation')
      : formatProfitCny(amount, locale)
  }
  return (
    <section
      aria-label={t('Model profit summary')}
      className='grid grid-cols-2 gap-4 rounded-xl border p-4 xl:grid-cols-4'
    >
      <StatCard
        title={t('Cash revenue')}
        value={cash('cash_revenue_cny')}
        description={t('Revenue attributed to customer-paid funds')}
        icon={Receipt}
        details={[
          {
            label: t('Balance charges'),
            value: formatProfitCny(data.revenue_cny, locale),
          },
        ]}
        compactMobile
      />
      <StatCard
        title={t('Known model procurement cost (estimate)')}
        value={
          data.upstream_cost_cny == null ||
          !Number.isFinite(data.upstream_cost_cny)
            ? t('Pending confirmation')
            : formatProfitCny(data.upstream_cost_cny, locale)
        }
        description={t(
          'Estimated from model purchase prices and recorded billable usage'
        )}
        icon={CircleDollarSign}
        tone='accent-2'
        details={[
          {
            label: t('Billing cost coverage'),
            value: `${formatNumber(data.cost_coverage * 100, locale)}%`,
          },
        ]}
        compactMobile
      />
      <StatCard
        title={t('Cash profit')}
        value={cash('cash_profit_cny')}
        description={t(
          'Consumed paid revenue minus procurement costs, including gifted consumption'
        )}
        icon={PiggyBank}
        details={[
          {
            label: t('Unattributed billing events'),
            value: formatNumber(
              data.funding_unknown_events ??
                data.billing_events ??
                data.known_cost_events + data.unknown_cost_events,
              locale
            ),
          },
        ]}
        compactMobile
      />
      <StatCard
        title={t('Gift balance consumed')}
        value={cash('gift_consumed_cny')}
        description={t(
          'Consumed gifts earn no cash revenue and still incur upstream costs'
        )}
        icon={Gift}
        details={[
          {
            label: t('Requests'),
            value: formatNumber(data.requests, locale),
          },
        ]}
        compactMobile
      />
    </section>
  )
}
