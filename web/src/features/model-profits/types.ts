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
export interface ProfitTotals {
  requests: number
  revenue_cny: number
  known_cost_revenue_cny: number
  upstream_cost_cny: number | null
  gross_profit_cny: number | null
  profit_margin: number | null
  known_cost_requests: number
  unknown_cost_requests: number
  known_cost_events: number
  unknown_cost_events: number
  cost_coverage: number
  pending_requests: number
  pending_revenue_cny: number
  refund_cny: number
}

export interface ModelProfitRow extends ProfitTotals {
  model_name: string
}

export interface ModelProfitResult {
  rows: ModelProfitRow[]
  summary: ProfitTotals
  timezone: string
  currency: string
  start_timestamp: number
  end_timestamp: number
  consumption_logging_enabled?: boolean
}

export interface ProfitFilters {
  start_timestamp: number
  end_timestamp: number
  model_name?: string
}

export interface CostRule {
  channel_id: number
  model_name: string
  effective_from: number
  effective_until?: number
  kind: 'request' | 'second' | 'image' | 'image_resolution' | 'token'
  unit_cost_cny?: number
  resolution_costs_cny?: Record<'1k' | '2k' | '4k', number>
  input_cost_per_million_cny?: number
  cache_cost_per_million_cny?: number
  output_cost_per_million_cny?: number
  deepseek_peak_multiplier?: 2
  source: string
  failure_refunded: boolean
}

export interface CostRules {
  schema_version: 1
  revision: number
  rules: CostRule[]
}
