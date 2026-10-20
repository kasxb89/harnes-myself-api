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
import { ArrowDownCircle, ArrowUpCircle, CreditCard, Gift } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { StatCard } from '@/features/dashboard/components/ui/stat-card'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { formatLedgerQuota } from './format'
import type { BalanceSummary } from './types'

export function LedgerSummary(props: {
  summary?: BalanceSummary
  loading: boolean
  error: boolean
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const data = props.summary
  let paidValue = '—'
  if (data) {
    if (data.paid_money !== null && data.paid_currency) {
      paidValue = `${formatNumber(data.paid_money, locale)} ${data.paid_currency}`
    } else if (data.paid_totals.length) {
      paidValue = data.paid_totals
        .map(
          (total) =>
            `${formatNumber(total.amount, locale)} ${total.currency || t('Currency not recorded')}${!total.currency && total.payment_provider ? ` · ${total.payment_provider}` : ''}`
        )
        .join(' / ')
    } else {
      paidValue = t('No paid orders')
    }
  }
  const giftsUnknown = (data?.registration_bonus_unknown ?? 0) > 0
  const cards = [
    {
      title: t('Actual payments received'),
      value: paidValue,
      description: t('Successful paid orders: {{count}}', {
        count: data?.paid_orders ?? 0,
      }),
      icon: CreditCard,
    },
    {
      title: t('Registration gifts'),
      value: giftsUnknown
        ? t('{{count}} gift records', {
            count: data?.registration_bonus_count ?? 0,
          })
        : formatLedgerQuota(data?.registration_bonus_quota, {
            locale,
            t,
            exact: data?.registration_bonus_quota_exact,
          }),
      description: giftsUnknown
        ? t('Historical gift amounts appear in the ledger below')
        : t('Registration gift records: {{count}}', {
            count: data?.registration_bonus_count ?? 0,
          }),
      icon: Gift,
    },
    {
      title: t('Administrator increases'),
      value: formatLedgerQuota(data?.admin_increase_quota, {
        locale,
        t,
        exact: data?.admin_increase_quota_exact,
      }),
      description: t('Known positive changes, including overrides'),
      icon: ArrowUpCircle,
    },
    {
      title: t('Administrator deductions'),
      value: formatLedgerQuota(data?.admin_decrease_quota, {
        locale,
        t,
        exact: data?.admin_decrease_quota_exact,
      }),
      description: t('Known deductions, including overrides'),
      icon: ArrowDownCircle,
    },
  ]
  return (
    <section aria-label={t('Balance summary')} className='space-y-2'>
      <div className='grid grid-cols-2 gap-4 rounded-xl border p-4 xl:grid-cols-4'>
        {cards.map((card) => (
          <StatCard
            key={card.title}
            {...card}
            loading={props.loading}
            error={props.error}
            compactMobile
          />
        ))}
      </div>
      <div className='text-muted-foreground space-y-1 text-xs'>
        <p>
          {t(
            'Totals cover all matching records, not only this page. Paid money and quota changes are counted separately.'
          )}
        </p>
        {data && data.unknown_count > 0 && (
          <p>
            {t(
              '{{count}} historical records lack raw quota amounts and are excluded from quota totals. Their recorded amounts remain visible below.',
              { count: data.unknown_count }
            )}
          </p>
        )}
        {data?.paid_totals.some((total) => !total.currency) && (
          <p>
            {t(
              'Some historical payments have no recorded currency. They are grouped by payment provider and must not be added across currencies.'
            )}
          </p>
        )}
        {data?.paid_totals
          .filter((total) => !total.currency)
          .map((total) => (
            <p key={total.payment_provider}>
              {total.payment_provider || t('Not recorded')}:{' '}
              {formatNumber(total.amount, locale)} ·{' '}
              {t('Currency not recorded')}
            </p>
          ))}
      </div>
    </section>
  )
}
