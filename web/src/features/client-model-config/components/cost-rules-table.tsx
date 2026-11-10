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

import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import {
  Table,
  TableHeader,
  TableBody,
  TableHead,
  TableRow,
  TableCell,
} from '@/components/ui/table'
import { PricingAmountInput } from '@/features/model-pricing/pricing-amount-input'
import { CostRuleForm } from '@/features/model-profits/components/cost-rule-form'
import { formatTimestampToDate } from '@/lib/format'

const CNY = { label: 'CNY', symbol: '¥', exchangeRate: 1 }
const COST_FIELDS: Record<string, string> = {
  unit_cost_cny: 'Unit cost',
  input_cost_per_million_cny: 'Input price',
  cache_cost_per_million_cny: 'Cache Read',
  output_cost_per_million_cny: 'Completion price',
  extra_image_cost_cny: 'Extra image cost',
}

export function CostRulesTable(props: {
  value: string
  onChange: (value: string) => void
  modelName: string
  channelId?: number
  disabled?: boolean
}) {
  const { t } = useTranslation()
  const [empty, setEmpty] = useState<{ source: string; keys: string[] }>({
    source: '',
    keys: [],
  })
  const emptyKeys = empty.source === props.value ? empty.keys : []
  const [adding, setAdding] = useState(false)
  let invalid = false
  let rules: Record<string, unknown>[] = []
  try {
    const parsed: unknown = JSON.parse(props.value)
    if (
      Array.isArray(parsed) &&
      parsed.every(
        (item) => item && typeof item === 'object' && !Array.isArray(item)
      )
    ) {
      rules = parsed
    } else invalid = true
  } catch {
    invalid = true
  }
  const rows = rules.flatMap((rule, index) => [
    ...Object.keys(COST_FIELDS)
      .filter((key) => typeof rule[key] === 'number')
      .map((key) => ({
        index,
        rule,
        key,
        label: t(COST_FIELDS[key]),
        resolution: false,
        value: Number(rule[key]),
      })),
    ...Object.entries(
      rule.resolution_costs_cny && typeof rule.resolution_costs_cny === 'object'
        ? rule.resolution_costs_cny
        : {}
    )
      .filter(([, value]) => typeof value === 'number')
      .map(([key, value]) => ({
        index,
        rule,
        key,
        label: key,
        resolution: true,
        value: Number(value),
      })),
  ])
  return (
    <div
      className='min-w-0 space-y-3'
      data-billing-invalid={emptyKeys.length > 0 || invalid || undefined}
    >
      {invalid && (
        <p role='alert'>
          {t(
            'Procurement costs could not be read. Existing costs are preserved.'
          )}
        </p>
      )}
      {rows.length ? (
        <Table aria-label={t('Procurement costs in CNY')}>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Channel ID')}</TableHead>
              <TableHead>{t('Billing item')}</TableHead>
              <TableHead className='min-w-36'>
                {t('Unit price (CNY)')}
              </TableHead>
              <TableHead>{t('Unit and allowance')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((row) => {
              const token = row.key.includes('per_million')
              const perSecond =
                row.rule.kind === 'h3_video' || row.rule.kind === 'second'
              let unit = perSecond ? t('second') : t('request')
              if (
                row.key === 'extra_image_cost_cny' ||
                row.rule.kind === 'image' ||
                row.rule.kind === 'image_resolution'
              ) {
                unit = t('image')
              }
              if (token) unit = t('1M tokens')
              return (
                <TableRow key={`${row.index}:${row.key}`}>
                  <TableCell>
                    #{String(row.rule.channel_id)}
                    <p className='text-muted-foreground text-xs'>
                      {formatTimestampToDate(Number(row.rule.effective_from))}
                    </p>
                  </TableCell>
                  <TableCell>{row.label}</TableCell>
                  <TableCell>
                    <PricingAmountInput
                      currency={CNY}
                      aria-label={`${t('Procurement cost')} #${String(row.rule.channel_id)} · ${row.label}`}
                      value={
                        emptyKeys.includes(`${row.index}:${row.key}`)
                          ? ''
                          : row.value
                      }
                      disabled={props.disabled}
                      onChange={(value) => {
                        const key = `${row.index}:${row.key}`
                        if (!value) {
                          setEmpty({
                            source: props.value,
                            keys: [...new Set([...emptyKeys, key])],
                          })
                          return
                        }
                        const next = rules.map((rule, index) => {
                          if (index !== row.index) return rule
                          if (row.resolution) {
                            return {
                              ...rule,
                              resolution_costs_cny: {
                                ...(rule.resolution_costs_cny as Record<
                                  string,
                                  number
                                >),
                                [row.key]: Number(value),
                              },
                            }
                          }
                          return { ...rule, [row.key]: Number(value) }
                        })
                        const source = JSON.stringify(next, null, 2)
                        setEmpty({
                          source,
                          keys: emptyKeys.filter((item) => item !== key),
                        })
                        props.onChange(source)
                      }}
                    />
                  </TableCell>
                  <TableCell className='whitespace-normal'>
                    ¥/{unit}
                    {typeof row.rule.free_input_images === 'number' && (
                      <p className='text-muted-foreground text-xs'>
                        {t(
                          'First {{count}} units are free; only the excess is charged.',
                          { count: row.rule.free_input_images }
                        )}
                      </p>
                    )}
                    {typeof row.rule.long_video_multiplier === 'number' && (
                      <p className='text-muted-foreground text-xs'>
                        {t('Long-video multiplier: {{value}}×', {
                          value: row.rule.long_video_multiplier,
                        })}
                      </p>
                    )}
                  </TableCell>
                </TableRow>
              )
            })}
          </TableBody>
        </Table>
      ) : (
        <p className='text-muted-foreground text-sm'>
          {t(
            'No procurement cost records. Add the verified cost before publishing.'
          )}
        </p>
      )}
      {emptyKeys.length > 0 && (
        <p role='alert' className='text-destructive text-sm'>
          {t('Enter a price before saving. Use 0 only when this item is free.')}
        </p>
      )}
      <Button
        type='button'
        variant='outline'
        disabled={props.disabled || invalid}
        onClick={() => setAdding(true)}
      >
        {t('Add cost rule')}
      </Button>
      <Dialog open={adding} onOpenChange={setAdding}>
        <DialogContent className='max-h-[85dvh] overflow-y-auto sm:max-w-2xl'>
          <DialogHeader>
            <DialogTitle>{t('Add cost rule')}</DialogTitle>
          </DialogHeader>
          <CostRuleForm
            modelName={props.modelName}
            channelId={props.channelId}
            saving={false}
            onSave={async (rule) => {
              props.onChange(JSON.stringify([...rules, rule], null, 2))
              setAdding(false)
            }}
          />
        </DialogContent>
      </Dialog>
    </div>
  )
}
