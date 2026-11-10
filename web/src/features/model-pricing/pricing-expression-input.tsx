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
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Textarea } from '@/components/ui/textarea'

import type { PricingCurrency } from './currency'
import { convertExpressionCurrency } from './expression-currency'

/** The parent retains USD; this editor presents a monetary expression in CNY. */
export function PricingExpressionInput(props: {
  value: string
  currency: PricingCurrency
  onChange: (usd: string) => void
  rows?: number
}) {
  const { t } = useTranslation()
  const errorId = useId()
  const [draft, setDraft] = useState<{
    text: string
    source: string
    rate: number
    invalid: boolean
  } | null>(null)
  const rate = props.currency.exchangeRate
  const converted = convertExpressionCurrency(props.value, rate)
  const active =
    draft?.source === props.value && draft.rate === rate ? draft : null
  const invalid = active?.invalid || converted === null
  return (
    <div className='space-y-2' data-billing-invalid={invalid || undefined}>
      <Textarea
        aria-label={t('Billing expression in {{currency}}', {
          currency: props.currency.label,
        })}
        aria-invalid={invalid || undefined}
        aria-describedby={invalid ? errorId : undefined}
        value={active?.text ?? converted ?? ''}
        onChange={(event) => {
          const text = event.target.value
          const usd = convertExpressionCurrency(text, 1 / rate)
          const next = usd ?? text
          setDraft({
            text,
            source: next,
            rate,
            invalid: usd === null || !text.trim(),
          })
          props.onChange(next)
        }}
        rows={props.rows ?? 7}
        className='font-mono text-xs'
        spellCheck={false}
      />
      {invalid && (
        <p id={errorId} role='alert' className='text-destructive text-sm'>
          {t(
            'This expression cannot be converted safely. Existing prices are preserved; correct the expression before saving.'
          )}
        </p>
      )}
    </div>
  )
}
