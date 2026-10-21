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

import { profitChartRows, profitModelOptions } from '../lib/chart-data'
import type { ModelProfitResult, ModelProfitRow } from '../types'

const row = (model: string, revenue: number, requests: number) =>
  ({
    model_name: model,
    revenue_cny: revenue,
    requests,
  }) as ModelProfitRow

describe('profit dashboard data', () => {
  it('preserves the revenue and request totals when smaller models are grouped, including refunds', () => {
    const rows = [
      row('low', 2, 8),
      row('high', 9, 1),
      row('refund', -3, 0),
      row('middle', 5, 3),
    ]
    const result = profitChartRows(rows, 'revenue_cny', 2)
    expect(result.map((item) => item.model_name)).toEqual([
      'high',
      'middle',
      '',
    ])
    expect(result.at(-1)).toMatchObject({
      other: true,
      revenue_cny: -1,
      requests: 8,
    })
    expect(result.reduce((total, item) => total + item.revenue_cny, 0)).toBe(13)
    expect(result.reduce((total, item) => total + item.requests, 0)).toBe(12)
    expect(rows[0].model_name).toBe('low')
  })

  it('ranks request counts independently of revenue', () => {
    expect(
      profitChartRows(
        [row('expensive', 90, 1), row('frequent', 1, 40)],
        'requests',
        5
      ).map((item) => item.model_name)
    ).toEqual(['frequent', 'expensive'])
  })

  it('uses the server historical catalog instead of reducing it to filtered rows', () => {
    const result = {
      model_options: ['retired', 'flash', 'flash-mini', 'flash'],
      rows: [row('flash', 1, 1)],
    } as ModelProfitResult
    expect(profitModelOptions(result, 'selected-previous-period')).toEqual([
      'flash',
      'flash-mini',
      'retired',
      'selected-previous-period',
    ])
    expect(profitModelOptions(undefined, '')).toEqual([])
  })
})
