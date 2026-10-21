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
import { z } from 'zod'

import type { CostRule } from '../types'
import { shanghaiDateTime } from './period'

export const costRuleSchema = z
  .object({
    channel_id: z.string().regex(/^[1-9]\d*$/, 'Enter a positive channel ID'),
    model_name: z.string().trim().min(1, 'Enter an exact model name').max(200),
    effective_from: z.string().min(1, 'Enter an effective time'),
    effective_until: z.string().optional(),
    kind: z.enum(['request', 'second', 'image', 'image_resolution', 'token']),
    unit_cost_cny: z.string(),
    resolution_1k_cost_cny: z.string(),
    resolution_2k_cost_cny: z.string(),
    resolution_4k_cost_cny: z.string(),
    input_cost_per_million_cny: z.string(),
    cache_cost_per_million_cny: z.string(),
    output_cost_per_million_cny: z.string(),
    deepseek_peak_multiplier: z.string(),
    source: z.string().trim().min(1, 'Enter a supplier price source').max(512),
    failure_refunded: z.boolean(),
  })
  .superRefine((value, context) => {
    if (
      !Number.isSafeInteger(Number(value.channel_id)) ||
      Number(value.channel_id) > 1000000000
    ) {
      context.addIssue({
        code: 'custom',
        path: ['channel_id'],
        message: 'Enter a positive channel ID',
      })
    }
    const effective = Date.parse(`${value.effective_from}:00+08:00`)
    if (
      !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(value.effective_from) ||
      !Number.isFinite(effective) ||
      effective < 0 ||
      shanghaiDateTime(effective) !== value.effective_from
    ) {
      context.addIssue({
        code: 'custom',
        path: ['effective_from'],
        message: 'Enter an effective time',
      })
    }
    if (value.effective_until) {
      const until = Date.parse(`${value.effective_until}:00+08:00`)
      if (
        !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(value.effective_until) ||
        !Number.isFinite(until) ||
        until <= effective ||
        shanghaiDateTime(until) !== value.effective_until
      ) {
        context.addIssue({
          code: 'custom',
          path: ['effective_until'],
          message: 'End time must follow the effective time',
        })
      }
    }
    let rateFields: Array<
      | 'unit_cost_cny'
      | 'resolution_1k_cost_cny'
      | 'resolution_2k_cost_cny'
      | 'resolution_4k_cost_cny'
      | 'input_cost_per_million_cny'
      | 'cache_cost_per_million_cny'
      | 'output_cost_per_million_cny'
    > = ['unit_cost_cny']
    if (value.kind === 'token') {
      rateFields = [
        'input_cost_per_million_cny',
        'cache_cost_per_million_cny',
        'output_cost_per_million_cny',
      ]
    } else if (value.kind === 'image_resolution') {
      rateFields = [
        'resolution_1k_cost_cny',
        'resolution_2k_cost_cny',
        'resolution_4k_cost_cny',
      ]
    }
    for (const name of rateFields) {
      const rate = Number(value[name])
      if (
        value[name].trim() === '' ||
        !Number.isFinite(rate) ||
        rate < 0 ||
        rate > 1000000
      ) {
        context.addIssue({
          code: 'custom',
          path: [name],
          message: 'Enter a non-negative CNY price',
        })
      }
    }
    if (
      value.kind === 'token' &&
      value.deepseek_peak_multiplier.trim() !== '' &&
      Number(value.deepseek_peak_multiplier) !== 2
    ) {
      context.addIssue({
        code: 'custom',
        path: ['deepseek_peak_multiplier'],
        message: 'Use 2 for the DeepSeek peak multiplier, or leave blank',
      })
    }
  })

export type CostRuleFormValues = z.infer<typeof costRuleSchema>

export function toCostRule(value: CostRuleFormValues): CostRule {
  const rule: CostRule = {
    channel_id: Number(value.channel_id),
    model_name: value.model_name.trim(),
    effective_from: Date.parse(`${value.effective_from}:00+08:00`) / 1000,
    kind: value.kind,
    source: value.source.trim(),
    failure_refunded: value.failure_refunded,
  }
  if (value.effective_until) {
    rule.effective_until =
      Date.parse(`${value.effective_until}:00+08:00`) / 1000
  }
  if (value.kind === 'token') {
    rule.input_cost_per_million_cny = Number(value.input_cost_per_million_cny)
    rule.cache_cost_per_million_cny = Number(value.cache_cost_per_million_cny)
    rule.output_cost_per_million_cny = Number(value.output_cost_per_million_cny)
    if (value.deepseek_peak_multiplier.trim() !== '') {
      rule.deepseek_peak_multiplier = 2
    }
  } else if (value.kind === 'image_resolution') {
    rule.resolution_costs_cny = {
      '1k': Number(value.resolution_1k_cost_cny),
      '2k': Number(value.resolution_2k_cost_cny),
      '4k': Number(value.resolution_4k_cost_cny),
    }
  } else rule.unit_cost_cny = Number(value.unit_cost_cny)
  return rule
}
