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

import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { toIntlLocale } from '@/i18n/languages'

import { formatProfitCny } from '../lib/format'

/** One accounting explanation shared by the report and operations details. */
export function ProfitFormulas() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  return (
    <Collapsible>
      <CollapsibleTrigger render={<Button variant='link' size='sm' />}>
        {t('Calculation notes')}
      </CollapsibleTrigger>
      <CollapsibleContent
        role='region'
        aria-label={t('Profit formulas')}
        className='pt-3'
      >
        <div className='flex flex-col gap-3'>
          <ol className='flex list-inside list-decimal flex-col gap-2 text-sm break-words'>
            <li>
              {t(
                'Model procurement cost (estimate) = Σ(final billable usage × applicable purchase unit price).'
              )}
            </li>
            <li>
              {t(
                'Paid revenue = Σ(consumed paid face value × original cash received ÷ original credited face value).'
              )}
            </li>
            <li>
              {t(
                'Actual model profit (estimate) = paid consumption revenue − total model procurement cost (estimate).'
              )}
            </li>
            <li>
              {t(
                'Settled net consumption = paid balance consumed + gifted balance consumed + unverified-source balance consumed.'
              )}
            </li>
          </ol>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Purchase prices follow the original channel, model, specification and effective date. Input, cached input and output tokens are calculated separately per million; videos, images and requests use their billable seconds, image counts and request counts.'
            )}
          </p>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Gift consumption earns zero cash revenue, but its procurement cost is still deducted. Recharge receipts, unused gifts and unused supplier prepayments are not consumed-model profit.'
            )}
          </p>
          <p className='text-muted-foreground text-xs'>
            {t(
              'FIFO funding: oldest credits are consumed first. Unverified opening balances stay unverified; refunds return to the original credit lots.'
            )}
          </p>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Pending reservations are excluded until settlement. Refunds reverse the original funding allocation; unknown historical funding or missing procurement prices remain pending confirmation, never zero.'
            )}
          </p>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Discount illustration: {{paid}} paid for {{credited}} credit; consuming {{used}} records {{revenue}} paid revenue.',
              {
                paid: formatProfitCny(80, locale),
                credited: formatProfitCny(100, locale),
                used: formatProfitCny(10, locale),
                revenue: formatProfitCny(8, locale),
              }
            )}
          </p>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Illustration only: a charge of {{charge}} with procurement cost {{cost}} produces {{paidProfit}} profit when fully paid, or {{giftProfit}} when fully gifted. Operating expenses and taxes are excluded.',
              {
                charge: formatProfitCny(0.3, locale),
                cost: formatProfitCny(0.15, locale),
                paidProfit: formatProfitCny(0.15, locale),
                giftProfit: formatProfitCny(-0.15, locale),
              }
            )}
          </p>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Usage count means original calls. Unit prices are period averages per call after settlement and refunds, not current token, second or image tariffs. Pending, incomplete or refund-only periods remain unconfirmed.'
            )}
          </p>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Upstream costs are estimated from configured supplier prices, not reconciled supplier deductions or invoices.'
            )}
          </p>
        </div>
      </CollapsibleContent>
    </Collapsible>
  )
}
