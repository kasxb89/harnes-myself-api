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
import { formatNumber } from '@/lib/format'

import {
  formatProfitCny,
  verifiedFundingAmount,
  type ConfirmedFundingField,
} from '../lib/format'
import type { ProfitTotals } from '../types'

const fields: { field: ConfirmedFundingField; labelKey: string }[] = [
  {
    field: 'confirmed_cash_revenue_cny',
    labelKey: 'Confirmed paid consumption revenue',
  },
  {
    field: 'confirmed_paid_consumed_cny',
    labelKey: 'Consumed paid face value',
  },
  {
    field: 'confirmed_gift_consumed_cny',
    labelKey: 'Consumed gifted face value',
  },
  {
    field: 'unknown_funding_consumed_cny',
    labelKey: 'Consumed face value awaiting attribution',
  },
]

const accountingReasonLabels: Record<string, string> = {
  procurement_price_missing:
    'Procurement price missing for this channel, model or effective period',
  image_resolution_missing: 'Original billed image resolution is missing',
  invalid_usage_facts: 'Recorded billable usage is invalid',
  usage_or_settlement_evidence_missing:
    'Actual usage or settlement evidence is missing',
  pending_settlement: 'Awaiting settlement',
  historical_funding_evidence_missing:
    'Historical paid-versus-gift allocation evidence is missing',
  payment_receipt_or_source_unverified:
    'Cash receipt amount, currency or funding source is unverified',
}

export function ProfitFundingDetails(props: { data: ProfitTotals }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  return (
    <section
      aria-label={t('Funding allocation details')}
      className='space-y-2 rounded-lg border p-3'
    >
      <h3 className='text-sm font-medium'>{t('Funding allocation details')}</h3>
      <dl className='grid gap-3 sm:grid-cols-2 xl:grid-cols-4'>
        {fields.map((item) => {
          const value = verifiedFundingAmount(props.data, item.field)
          return (
            <div key={item.field}>
              <dt className='text-muted-foreground text-xs'>
                {t(item.labelKey)}
              </dt>
              <dd className='mt-1 font-medium tabular-nums'>
                {value === null
                  ? t('Pending confirmation')
                  : formatProfitCny(value, locale)}
              </dd>
            </div>
          )
        })}
      </dl>
      {props.data.gross_profit_cny !== null &&
        Number.isFinite(props.data.gross_profit_cny) && (
          <p className='text-sm'>
            {t('Book margin on cost-covered consumption')}:{' '}
            <strong className='tabular-nums'>
              {formatProfitCny(props.data.gross_profit_cny, locale)}
            </strong>
            <span className='text-muted-foreground ml-2 text-xs'>
              {t(
                'Balance charges minus known procurement costs; this is not confirmed cash profit.'
              )}
            </span>
          </p>
        )}
      {[
        {
          label: t('Missing cost evidence'),
          reasons: props.data.unknown_cost_reasons,
        },
        {
          label: t('Missing paid-versus-gift evidence'),
          reasons: props.data.unconfirmed_funding_reasons,
        },
      ].map((group) => {
        const entries = Object.entries(group.reasons ?? {}).filter(
          ([, count]) => Number.isSafeInteger(count) && count > 0
        )
        if (!entries.length) return null
        return (
          <section
            key={group.label}
            aria-label={group.label}
            className='space-y-1 text-xs'
          >
            <h4 className='font-medium'>{group.label}</h4>
            <ul className='text-muted-foreground space-y-1'>
              {entries.map(([reason, count]) => (
                <li key={reason}>
                  {t(
                    Object.hasOwn(accountingReasonLabels, reason)
                      ? accountingReasonLabels[reason]
                      : 'Other missing accounting evidence'
                  )}
                  :{' '}
                  <span className='tabular-nums'>
                    {formatNumber(count, locale)}
                  </span>
                </li>
              ))}
            </ul>
          </section>
        )
      })}
      <p className='text-muted-foreground text-xs'>
        {t(
          'Confirmed settled parts only; unverified consumption and pending reservations are not silently treated as paid revenue.'
        )}
      </p>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Cash revenue is separate from face-value consumption and must not be added to it. The three face-value amounts sum to net consumption. Missing cash receipt amounts or CNY evidence keep total profit pending confirmation.'
        )}
      </p>
    </section>
  )
}
