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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import {
  Table,
  TableHeader,
  TableBody,
  TableHead,
  TableRow,
  TableCell,
} from '@/components/ui/table'
import { BILLING_VARS } from '@/features/pricing/lib/billing-expr'
import { formatBillingCondition } from '@/features/pricing/lib/billing-expression/condition-display'
import { evaluateBillingExpression } from '@/features/pricing/lib/billing-expression/runtime'
import type { ExpressionNode } from '@/features/pricing/lib/billing-expression/types'
import { getTaskUsagePriceUnitLabelKey } from '@/features/pricing/lib/dynamic-price'
import {
  taskPriceLabel,
  taskUsageUnitLabel,
} from '@/features/pricing/lib/task-price-display'
import type { BillingUsageSchema } from '@/features/pricing/types'

import type { PricingCurrency } from './currency'
import {
  readExpressionPriceFields,
  replaceExpressionPrice,
} from './expression-price-fields'
import { PricingAmountInput } from './pricing-amount-input'

export function ExpressionPriceTable(props: {
  expression: string
  currency: PricingCurrency
  usageSchema?: BillingUsageSchema
  onChange: (expression: string) => void
  readOnly?: boolean
  onDraftChange?: () => void
}) {
  const { t, i18n } = useTranslation()
  const [empty, setEmpty] = useState<{ source: string; indices: number[] }>({
    source: '',
    indices: [],
  })
  const emptyIndices = empty.source === props.expression ? empty.indices : []
  const fields = readExpressionPriceFields(props.expression, props.usageSchema)
  if (!fields) {
    return (
      <ErrorState
        title={t(
          'These prices cannot yet be edited in the table. Existing prices are preserved.'
        )}
      />
    )
  }
  const quantityLabel = (field: string) => {
    const variable = BILLING_VARS.find((item) => item.key === field)
    let fallback = variable ? t(variable.label) : field
    if (field === 'long_video') fallback = t('Long video')
    if (field === 'image_count') fallback = t('Image count')
    return taskPriceLabel(
      props.usageSchema?.[field]?.description,
      fallback,
      i18n.language
    )
  }
  const describe = (node: ExpressionNode): string => {
    if (node.kind === 'literal') return String(node.value)
    if (node.kind === 'variable') return quantityLabel(node.name)
    if (
      node.kind === 'call' &&
      node.name === 'u' &&
      node.args[0].kind === 'literal'
    ) {
      return quantityLabel(String(node.args[0].value))
    }
    if (node.kind === 'unary' && node.operator === '!') {
      return t('Not {{condition}}', { condition: describe(node.operand) })
    }
    if (
      node.kind === 'binary' &&
      ['==', '!=', '<', '<=', '>', '>=', '&&', '||'].includes(node.operator)
    ) {
      const operator =
        {
          '==': '=',
          '!=': '≠',
          '<=': '≤',
          '>=': '≥',
          '&&': t('and'),
          '||': t('or'),
        }[node.operator] ?? node.operator
      return `${describe(node.left)} ${operator} ${describe(node.right)}`
    }
    return (
      formatBillingCondition(
        props.expression.slice(node.start, node.end),
        t,
        i18n.language
      ) ?? t('Existing pricing condition')
    )
  }
  const notes = [
    ...new Map(
      fields
        .flatMap((field) => field.adjustments)
        .map((node) => [node.start, node])
    ).values(),
  ].map((node) => {
    if (node.kind !== 'conditional') {
      return t('Existing request adjustments remain unchanged.')
    }
    const yes = evaluateBillingExpression(
      props.expression.slice(node.yes.start, node.yes.end)
    )
    const no = evaluateBillingExpression(
      props.expression.slice(node.no.start, node.no.end)
    )
    return yes.status === 'success' && no.status === 'success'
      ? t(
          '{{condition}}: matching requests use {{yes}}×; other requests use {{no}}×.',
          { condition: describe(node.condition), yes: yes.cost, no: no.cost }
        )
      : t('Existing request adjustments remain unchanged.')
  })
  return (
    <div
      className='min-w-0 space-y-3'
      data-billing-invalid={emptyIndices.length > 0 || undefined}
    >
      <p className='text-muted-foreground text-sm'>
        {t(
          'Enter prices in CNY directly. Existing billing conditions and free allowances stay unchanged.'
        )}
      </p>
      {notes.map((note) => (
        <p className='text-sm' key={note}>
          {note}
        </p>
      ))}
      <Table aria-label={t('CNY prices')}>
        <TableHeader>
          <TableRow>
            <TableHead>{t('Pricing conditions')}</TableHead>
            <TableHead>{t('Billing item')}</TableHead>
            <TableHead className='min-w-36'>{t('Unit price (CNY)')}</TableHead>
            <TableHead>{t('Unit and allowance')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {fields.map((field, index) => {
            const definition = field.quantity
              ? props.usageSchema?.[field.quantity.field]
              : undefined
            let item = field.fixed ? t('Fixed price') : t('Additional charge')
            if (field.quantity) item = quantityLabel(field.quantity.field)
            const visibleConditions = field.conditions.filter(
              ({ node }) =>
                !(
                  node.kind === 'binary' &&
                  node.left.kind === 'call' &&
                  node.left.name === 'u' &&
                  node.left.args[0].kind === 'literal' &&
                  props.usageSchema?.[
                    String(node.left.args[0].value)
                  ]?.enum?.includes(field.tier)
                )
            )
            const conditions =
              [
                field.tier,
                ...visibleConditions.map(({ node, matches }) =>
                  matches
                    ? describe(node)
                    : t('Not {{condition}}', { condition: describe(node) })
                ),
              ]
                .filter(Boolean)
                .join(' · ') || t('All requests')
            let unit = t('request')
            if (field.quantity) {
              unit = t('1M tokens')
              if (definition) {
                unit = taskUsageUnitLabel(
                  definition,
                  i18n.language,
                  t(getTaskUsagePriceUnitLabelKey(definition.unit))
                )
              }
              if (field.quantity.field === 'image_count') unit = t('image')
            }
            if (field.quantity?.block !== undefined) {
              unit = `${field.quantity.block} ${unit}`
            }
            return (
              <TableRow
                key={`${field.node.start}:${field.quantity?.field ?? 'base'}`}
              >
                <TableCell className='max-w-72 whitespace-normal'>
                  {conditions}
                </TableCell>
                <TableCell className='whitespace-normal'>{item}</TableCell>
                <TableCell>
                  <PricingAmountInput
                    aria-label={`${conditions} · ${item}`}
                    value={emptyIndices.includes(index) ? '' : field.amount}
                    onInput={props.onDraftChange}
                    currency={props.currency}
                    disabled={props.readOnly}
                    className='min-w-28 tabular-nums'
                    onChange={(value) => {
                      props.onDraftChange?.()
                      if (!value) {
                        setEmpty({
                          source: props.expression,
                          indices: [...new Set([...emptyIndices, index])],
                        })
                        return
                      }
                      const next = replaceExpressionPrice(
                        props.expression,
                        field,
                        Number(value)
                      )
                      if (next !== null) {
                        setEmpty({
                          source: next,
                          indices: emptyIndices.filter(
                            (item) => item !== index
                          ),
                        })
                        props.onChange(next)
                      }
                    }}
                  />
                </TableCell>
                <TableCell className='whitespace-normal'>
                  <span>¥/{unit}</span>
                  {field.quantity?.free !== undefined && (
                    <p className='text-muted-foreground text-xs'>
                      {t(
                        'First {{count}} units are free; only the excess is charged.',
                        { count: field.quantity.free }
                      )}
                    </p>
                  )}
                  {field.quantity?.block !== undefined && (
                    <p className='text-muted-foreground text-xs'>
                      {t('Usage is rounded up in blocks of {{count}} units.', {
                        count: field.quantity.block,
                      })}
                    </p>
                  )}
                </TableCell>
              </TableRow>
            )
          })}
        </TableBody>
      </Table>
      {emptyIndices.length > 0 && (
        <p role='alert' className='text-destructive text-sm'>
          {t('Enter a price before saving. Use 0 only when this item is free.')}
        </p>
      )}
    </div>
  )
}
