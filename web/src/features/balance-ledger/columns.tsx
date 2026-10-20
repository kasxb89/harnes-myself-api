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

import { StatusBadge } from '@/components/status-badge'
import { toIntlLocale } from '@/i18n/languages'
import dayjs from '@/lib/dayjs'
import { formatNumber } from '@/lib/format'

import { BALANCE_ACTIONS, BALANCE_CATEGORIES } from './constants'
import { formatLedgerQuota } from './format'
import type { BalanceEntry } from './types'

export function useBalanceColumns(): ColumnDef<BalanceEntry>[] {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  return useMemo(
    () => [
      {
        id: 'time',
        header: t('Time'),
        cell: ({ row }) =>
          row.original.created_at > 0 ? (
            <time
              className='text-xs whitespace-nowrap tabular-nums'
              dateTime={new Date(row.original.created_at * 1000).toISOString()}
            >
              {dayjs
                .unix(row.original.created_at)
                .format('YYYY-MM-DD HH:mm:ss Z')}
            </time>
          ) : (
            <span>{t('Not recorded')}</span>
          ),
        size: 210,
      },
      {
        id: 'user',
        header: t('User'),
        cell: ({ row }) => (
          <div className='max-w-40 min-w-0 text-sm'>
            <div className='truncate font-medium' title={row.original.username}>
              {row.original.username || t('Not recorded')}
            </div>
            <div className='text-muted-foreground text-xs'>
              ID {formatNumber(row.original.user_id, locale)}
            </div>
          </div>
        ),
        size: 160,
      },
      {
        id: 'category',
        header: t('Balance source'),
        cell: ({ row }) => (
          <div className='space-y-1 text-xs'>
            <StatusBadge
              label={t(BALANCE_CATEGORIES[row.original.category])}
              variant={
                row.original.category === 'online_topup' ? 'success' : 'info'
              }
              copyable={false}
            />
            {BALANCE_ACTIONS[row.original.action] && (
              <div>{t(BALANCE_ACTIONS[row.original.action])}</div>
            )}
          </div>
        ),
        size: 210,
      },
      {
        id: 'payment',
        header: t('Actual payment'),
        cell: ({ row }) => {
          if (row.original.paid_money === null) return <span>—</span>
          return (
            <div className='space-y-1 text-xs tabular-nums'>
              <div>
                {formatNumber(row.original.paid_money, locale)}{' '}
                {row.original.paid_currency || t('Currency not recorded')}
              </div>
              <div className='text-muted-foreground break-all'>
                {row.original.payment_provider}
              </div>
            </div>
          )
        },
        size: 190,
      },
      {
        id: 'change',
        header: t('Net quota change'),
        cell: ({ row }) => {
          const entry = row.original
          if (entry.quota_delta === null) {
            return (
              <div className='space-y-1 text-xs'>
                <div className='text-muted-foreground'>{t('Not recorded')}</div>
                {entry.display_amount && (
                  <div className='max-w-64 break-words'>
                    {t('Historical recorded amount')}: {entry.display_amount}
                  </div>
                )}
              </div>
            )
          }
          return (
            <div className='space-y-1 text-xs tabular-nums'>
              <div className='font-medium'>
                {formatLedgerQuota(entry.quota_delta, {
                  locale,
                  t,
                  exact: entry.quota_delta_exact,
                  signed: true,
                })}
              </div>
              {entry.quota_before !== null && entry.quota_after !== null && (
                <div className='text-muted-foreground'>
                  {t('Before and after')}:{' '}
                  {formatLedgerQuota(entry.quota_before, {
                    locale,
                    t,
                    exact: entry.quota_before_exact,
                  })}
                  {' → '}
                  {formatLedgerQuota(entry.quota_after, {
                    locale,
                    t,
                    exact: entry.quota_after_exact,
                  })}
                </div>
              )}
            </div>
          )
        },
        size: 280,
      },
      {
        id: 'operator',
        header: t('Operator'),
        cell: ({ row }) => (
          <div className='max-w-40 space-y-1 text-xs break-words'>
            <div>{row.original.operator_username || t('Not recorded')}</div>
            {row.original.operator_id !== null && (
              <div className='text-muted-foreground'>
                ID {formatNumber(row.original.operator_id, locale)}
              </div>
            )}
          </div>
        ),
        size: 160,
      },
      {
        id: 'description',
        header: t('Description'),
        cell: ({ row }) => {
          const entry = row.original
          const actionLabel = BALANCE_ACTIONS[entry.action]
          let description = entry.content || '—'
          if (actionLabel) {
            description = `${t('Adjustment mode')}: ${t(actionLabel)}`
          } else if (entry.category !== 'other') {
            description = `${t('Balance source')}: ${t(BALANCE_CATEGORIES[entry.category])}`
          }
          return (
            <div className='max-w-96 space-y-1 text-xs [overflow-wrap:anywhere] break-words'>
              <div>{description}</div>
              {entry.legacy && (
                <div className='text-muted-foreground'>
                  {t('Historical record')}
                </div>
              )}
            </div>
          )
        },
        size: 320,
      },
    ],
    [locale, t]
  )
}
