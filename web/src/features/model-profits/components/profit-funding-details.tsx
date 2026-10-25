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

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import {
  formatProfitCny,
  verifiedFundingAmount,
  verifiedCashAmount,
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
  const unconfirmed = verifiedCashAmount(props.data, 'cash_profit_cny') === null
  const reasonGroups = [
    {
      label: t('Missing cost evidence'),
      reasons: props.data.unknown_cost_reasons,
    },
    {
      label: t('Missing paid-versus-gift evidence'),
      reasons: props.data.unconfirmed_funding_reasons,
    },
  ]
  return (
    <section
      aria-label={t('Funding allocation details')}
      className='flex flex-col gap-3'
    >
      <dl className='grid gap-x-5 gap-y-3 text-sm sm:grid-cols-2 xl:grid-cols-4'>
        {fields.map((item) => {
          const value = verifiedFundingAmount(props.data, item.field)
          return (
            <div key={item.field} className='min-w-0'>
              <dt className='text-muted-foreground text-xs'>
                {t(item.labelKey)}
              </dt>
              <dd className='mt-1 font-medium break-words tabular-nums'>
                {value === null
                  ? t('Pending confirmation')
                  : formatProfitCny(value, locale)}
              </dd>
            </div>
          )
        })}
      </dl>
      <Collapsible>
        <div className='flex flex-wrap items-center gap-x-3 gap-y-2 text-xs'>
          {unconfirmed && (
            <Badge variant='secondary'>{t('Pending confirmation')}</Badge>
          )}
          {props.data.unknown_cost_events > 0 && (
            <span className='text-muted-foreground'>
              {t('Missing cost evidence')}:{' '}
              {formatNumber(props.data.unknown_cost_events, locale)}
            </span>
          )}
          {(props.data.funding_unknown_events ?? 0) > 0 && (
            <span className='text-muted-foreground'>
              {t('Missing paid-versus-gift evidence')}:{' '}
              {formatNumber(props.data.funding_unknown_events ?? 0, locale)}
            </span>
          )}
          {(props.data.pending_requests > 0 ||
            (props.data.pending_billing_events ?? 0) > 0) && (
            <span className='text-muted-foreground'>
              {t('Awaiting settlement')}:{' '}
              {formatNumber(
                props.data.pending_billing_events ??
                  props.data.pending_requests,
                locale
              )}
            </span>
          )}
          <CollapsibleTrigger render={<Button variant='link' size='xs' />}>
            {t('View accounting details')}
          </CollapsibleTrigger>
        </div>
        <CollapsibleContent className='pt-3'>
          <div className='text-muted-foreground flex flex-col gap-3 text-xs'>
            {props.data.gross_profit_cny !== null &&
              Number.isFinite(props.data.gross_profit_cny) && (
                <p>
                  {t('Book margin on cost-covered consumption')}:{' '}
                  <strong className='text-foreground tabular-nums'>
                    {formatProfitCny(props.data.gross_profit_cny, locale)}
                  </strong>{' '}
                  ·{' '}
                  {t(
                    'Balance charges minus known procurement costs; this is not confirmed cash profit.'
                  )}
                </p>
              )}
            {reasonGroups.map((group) => {
              const entries = Object.entries(group.reasons ?? {}).filter(
                ([, count]) => Number.isSafeInteger(count) && count > 0
              )
              if (!entries.length) return null
              return (
                <section
                  key={group.label}
                  aria-label={group.label}
                  className='flex flex-col gap-1'
                >
                  <h4 className='text-foreground font-medium'>{group.label}</h4>
                  <ul className='flex flex-col gap-1'>
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
            <p>
              {t(
                'Cash revenue is separate from face-value consumption and must not be added to it. The three face-value amounts sum to net consumption. Missing cash receipt amounts or CNY evidence keep total profit pending confirmation.'
              )}
            </p>
            {(props.data.pending_requests > 0 ||
              (props.data.pending_billing_events ?? 0) > 0 ||
              props.data.pending_billing_events == null) && (
              <p>
                {t(
                  'Billing events awaiting settlement: {{events}}; original calls: {{count}}; reserved balance: {{amount}}. Pending amounts are excluded from cash profit.',
                  {
                    events:
                      props.data.pending_billing_events == null
                        ? t('Pending confirmation')
                        : formatNumber(
                            props.data.pending_billing_events,
                            locale
                          ),
                    count: formatNumber(props.data.pending_requests, locale),
                    amount: formatProfitCny(
                      props.data.pending_revenue_cny,
                      locale
                    ),
                  }
                )}
              </p>
            )}
          </div>
        </CollapsibleContent>
      </Collapsible>
    </section>
  )
}
