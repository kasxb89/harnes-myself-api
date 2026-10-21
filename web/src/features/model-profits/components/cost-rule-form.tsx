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
import { zodResolver } from '@hookform/resolvers/zod'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Combobox } from '@/components/ui/combobox'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'

import {
  costRuleSchema,
  toCostRule,
  type CostRuleFormValues,
} from '../lib/cost-schema'
import { shanghaiDateTime } from '../lib/period'
import type { CostRule } from '../types'

type CostInputName = Exclude<
  keyof CostRuleFormValues,
  'kind' | 'failure_refunded'
>

export function CostRuleForm(props: {
  onSave: (rule: CostRule) => Promise<void>
  saving: boolean
}) {
  const { t } = useTranslation()
  const form = useForm<CostRuleFormValues>({
    resolver: zodResolver(costRuleSchema),
    defaultValues: {
      channel_id: '',
      model_name: '',
      effective_from: shanghaiDateTime(),
      effective_until: '',
      kind: 'request',
      unit_cost_cny: '',
      resolution_1k_cost_cny: '',
      resolution_2k_cost_cny: '',
      resolution_4k_cost_cny: '',
      input_cost_per_million_cny: '',
      cache_cost_per_million_cny: '',
      output_cost_per_million_cny: '',
      deepseek_peak_multiplier: '',
      source: '',
      failure_refunded: false,
    },
  })
  const kind = form.watch('kind')
  const fields: { name: CostInputName; label: string; type?: string }[] = [
    { name: 'channel_id', label: t('Channel ID'), type: 'number' },
    { name: 'model_name', label: t('Exact model name') },
    {
      name: 'effective_from',
      label: t('Effective from (Shanghai time)'),
      type: 'datetime-local',
    },
    {
      name: 'effective_until',
      label: t('Cost valid until (Shanghai time, exclusive, optional)'),
      type: 'datetime-local',
    },
  ]
  if (kind === 'token') {
    fields.push(
      {
        name: 'input_cost_per_million_cny',
        label: t('Input cost (CNY per million tokens)'),
        type: 'number',
      },
      {
        name: 'cache_cost_per_million_cny',
        label: t('Cache read cost (CNY per million tokens)'),
        type: 'number',
      },
      {
        name: 'output_cost_per_million_cny',
        label: t('Output cost (CNY per million tokens)'),
        type: 'number',
      },
      {
        name: 'deepseek_peak_multiplier',
        label: t('DeepSeek peak multiplier (optional)'),
        type: 'number',
      }
    )
  } else if (kind === 'image_resolution') {
    fields.push(
      {
        name: 'resolution_1k_cost_cny',
        label: t('1K image cost (CNY per image)'),
        type: 'number',
      },
      {
        name: 'resolution_2k_cost_cny',
        label: t('2K image cost (CNY per image)'),
        type: 'number',
      },
      {
        name: 'resolution_4k_cost_cny',
        label: t('4K image cost (CNY per image)'),
        type: 'number',
      }
    )
  } else {
    fields.push({
      name: 'unit_cost_cny',
      label: t('Upstream unit cost (CNY)'),
      type: 'number',
    })
  }
  fields.push({ name: 'source', label: t('Supplier price source') })
  return (
    <Form {...form}>
      <form
        className='space-y-4'
        onSubmit={form.handleSubmit(async (value) => {
          try {
            await props.onSave(toCostRule(value))
            form.reset()
          } catch {
            /* The mutation displays the server error and preserves the form. */
          }
        })}
      >
        <FormField
          control={form.control}
          name='kind'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Cost unit')}</FormLabel>
              <FormControl>
                <Combobox
                  value={field.value}
                  onValueChange={field.onChange}
                  options={[
                    { value: 'request', label: t('Per request') },
                    { value: 'second', label: t('Per second') },
                    { value: 'image', label: t('Per image') },
                    {
                      value: 'image_resolution',
                      label: t('Per image resolution'),
                    },
                    { value: 'token', label: t('Per million tokens') },
                  ]}
                />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />
        <div className='grid gap-4 sm:grid-cols-2'>
          {fields.map((config) => (
            <FormField
              key={config.name}
              control={form.control}
              name={config.name}
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{config.label}</FormLabel>
                  <FormControl>
                    <Input
                      {...field}
                      type={config.type}
                      step={config.type === 'number' ? 'any' : undefined}
                      min={config.type === 'number' ? 0 : undefined}
                      disabled={props.saving}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          ))}
        </div>
        <FormField
          control={form.control}
          name='failure_refunded'
          render={({ field }) => (
            <FormItem className='flex items-start gap-2'>
              <FormControl>
                <Checkbox
                  checked={field.value}
                  onCheckedChange={(value) => field.onChange(value === true)}
                  disabled={props.saving}
                />
              </FormControl>
              <div className='space-y-1'>
                <FormLabel>{t('Supplier refunds failed requests')}</FormLabel>
                <p className='text-muted-foreground text-xs'>
                  {t(
                    'Enable only when the supplier guarantees failure refunds.'
                  )}
                </p>
              </div>
              <FormMessage />
            </FormItem>
          )}
        />
        <Button type='submit' disabled={props.saving}>
          {props.saving ? t('Saving...') : t('Add cost version')}
        </Button>
      </form>
    </Form>
  )
}
