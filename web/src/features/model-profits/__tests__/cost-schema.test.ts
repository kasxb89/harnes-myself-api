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
import { describe, expect, it } from 'vitest'

import {
  costRuleSchema,
  toCostRule,
  type CostRuleFormValues,
} from '../lib/cost-schema'

const base: CostRuleFormValues = {
  channel_id: '19',
  model_name: 'exact-model',
  effective_from: '2026-10-21T12:30',
  kind: 'second',
  unit_cost_cny: '0.1',
  resolution_1k_cost_cny: '',
  resolution_2k_cost_cny: '',
  resolution_4k_cost_cny: '',
  input_cost_per_million_cny: '',
  cache_cost_per_million_cny: '',
  output_cost_per_million_cny: '',
  deepseek_peak_multiplier: '',
  source: 'Verified supplier price sheet',
  failure_refunded: true,
}

describe('supplier cost version input', () => {
  it('requires each resolution price and keeps explicitly free image tiers', () => {
    expect(
      costRuleSchema.safeParse({ ...base, kind: 'image_resolution' }).success
    ).toBe(false)
    const value = costRuleSchema.parse({
      ...base,
      kind: 'image_resolution',
      resolution_1k_cost_cny: '0',
      resolution_2k_cost_cny: '0.15',
      resolution_4k_cost_cny: '0.2',
    })
    const rule = toCostRule(value)
    expect(rule.resolution_costs_cny).toEqual({
      '1k': 0,
      '2k': 0.15,
      '4k': 0.2,
    })
    expect(rule).not.toHaveProperty('unit_cost_cny')
    expect(
      costRuleSchema.safeParse({ ...value, resolution_2k_cost_cny: '-1' })
        .success
    ).toBe(false)
    expect(
      costRuleSchema.safeParse({ ...value, resolution_4k_cost_cny: '1000001' })
        .success
    ).toBe(false)
  })
  it('preserves an explicitly free unit price and omits inactive token prices', () => {
    const value = costRuleSchema.parse({ ...base, unit_cost_cny: '0' })
    expect(toCostRule(value)).toEqual({
      channel_id: 19,
      model_name: 'exact-model',
      effective_from: Date.parse('2026-10-21T12:30:00+08:00') / 1000,
      kind: 'second',
      source: base.source,
      failure_refunded: true,
      unit_cost_cny: 0,
    })
  })
  it('requires all token prices and preserves an optional frozen-tier multiplier', () => {
    expect(costRuleSchema.safeParse({ ...base, kind: 'token' }).success).toBe(
      false
    )
    const result = toCostRule(
      costRuleSchema.parse({
        ...base,
        kind: 'token',
        input_cost_per_million_cny: '2',
        cache_cost_per_million_cny: '0.2',
        output_cost_per_million_cny: '3',
        deepseek_peak_multiplier: '2',
      })
    )
    expect(result.input_cost_per_million_cny).toBe(2)
    expect(result.deepseek_peak_multiplier).toBe(2)
    expect(result).not.toHaveProperty('unit_cost_cny')
  })
  it('bounds a temporary supplier quote in Shanghai time without reviving an older price', () => {
    const rule = toCostRule(
      costRuleSchema.parse({ ...base, effective_until: '2026-11-01T00:00' })
    )
    expect(rule.effective_until).toBe(
      Date.parse('2026-11-01T00:00:00+08:00') / 1000
    )
    expect(
      costRuleSchema.safeParse({
        ...base,
        effective_until: base.effective_from,
      }).success
    ).toBe(false)
    expect(
      costRuleSchema.safeParse({ ...base, effective_until: '2026-02-30T00:00' })
        .success
    ).toBe(false)
  })
  it.each([
    { channel_id: '0' },
    { channel_id: '9007199254740992' },
    { model_name: '  ' },
    { unit_cost_cny: '' },
    { unit_cost_cny: '-1' },
    { unit_cost_cny: 'Infinity' },
    { effective_from: '2026-02-30T12:30' },
    { source: '' },
  ])(
    'blocks invalid cost data before a new version can be saved: %j',
    (patch) => {
      expect(costRuleSchema.safeParse({ ...base, ...patch }).success).toBe(
        false
      )
    }
  )
})
