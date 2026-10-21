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
import { CircleDollarSign, Percent, PiggyBank, Receipt } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { StatCard } from '@/features/dashboard/components/ui/stat-card'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { formatProfitCny, formatProfitMargin } from '../lib/format'
import type { ProfitTotals } from '../types'

export function ProfitSummary(props: { data: ProfitTotals }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const data = props.data
  return (
    <section
      aria-label={t('Model profit summary')}
      className='grid grid-cols-2 gap-4 rounded-xl border p-4 xl:grid-cols-4'
    >
      <StatCard
        title={t('Billed revenue')}
        value={formatProfitCny(data.revenue_cny, locale)}
        description={t(
          'Balance charged at customer prices; includes gift balances'
        )}
        icon={Receipt}
        details={[
          { label: t('Requests'), value: formatNumber(data.requests, locale) },
        ]}
        compactMobile
      />
      <StatCard
        title={t('Known upstream cost (estimate)')}
        value={formatProfitCny(data.upstream_cost_cny, locale)}
        description={t('Costs supported by configured supplier price rules')}
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
        title={t('Gross profit (known-cost portion)')}
        value={formatProfitCny(data.gross_profit_cny, locale)}
        description={t('Revenue with known costs minus those upstream costs')}
        icon={PiggyBank}
        details={[
          {
            label: t('Revenue with known costs'),
            value: formatProfitCny(data.known_cost_revenue_cny, locale),
          },
        ]}
        compactMobile
      />
      <StatCard
        title={t('Gross margin (known-cost portion)')}
        value={formatProfitMargin(data.profit_margin, locale)}
        description={t('Calculated only for requests with known costs')}
        icon={Percent}
        details={[
          {
            label: t('Unknown cost events'),
            value: formatNumber(data.unknown_cost_events, locale),
          },
        ]}
        compactMobile
      />
    </section>
  )
}
