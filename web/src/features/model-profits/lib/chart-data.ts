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
import type { ModelProfitResult, ModelProfitRow } from '../types'

export interface ProfitChartRow {
  id: string
  model_name: string
  revenue_cny: number
  requests: number
  other: boolean
}

/** Every hidden model remains represented in the final Other models bucket. */
export function profitChartRows(
  rows: ModelProfitRow[],
  metric: 'revenue_cny' | 'requests',
  limit: number
): ProfitChartRow[] {
  const sorted = [...rows].sort(
    (a, b) => b[metric] - a[metric] || a.model_name.localeCompare(b.model_name)
  )
  const visible = sorted.slice(0, limit).map((row) => ({
    id: `model:${row.model_name}`,
    model_name: row.model_name,
    revenue_cny: row.revenue_cny,
    requests: row.requests,
    other: false,
  }))
  const remaining = sorted.slice(limit)
  if (remaining.length > 0) {
    visible.push({
      id: 'other',
      model_name: '',
      revenue_cny: remaining.reduce((total, row) => total + row.revenue_cny, 0),
      requests: remaining.reduce((total, row) => total + row.requests, 0),
      other: true,
    })
  }
  return visible
}

export function profitModelOptions(
  result: ModelProfitResult | undefined,
  selected: string
): string[] {
  const models =
    result?.model_options ?? result?.rows.map((row) => row.model_name) ?? []
  return [...new Set([...models, selected].filter(Boolean))].sort()
}
