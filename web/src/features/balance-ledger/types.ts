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
export type BalanceCategory =
  | 'all'
  | 'registration_bonus'
  | 'online_topup'
  | 'admin_adjustment'
  | 'redemption'
  | 'invitation_reward'
  | 'checkin_reward'
  | 'subscription_purchase'
  | 'other'

export interface BalanceFilters {
  p: number
  page_size: number
  category: BalanceCategory
  user_id?: number
  start_timestamp?: number
  end_timestamp?: number
}

export interface BalanceEntry {
  id: string
  created_at: number
  user_id: number
  username: string
  category: Exclude<BalanceCategory, 'all'>
  action: string
  quota_delta: number | null
  quota_delta_exact?: string | null
  quota_before: number | null
  quota_before_exact?: string | null
  quota_after: number | null
  quota_after_exact?: string | null
  paid_money: number | null
  paid_currency: string | null
  payment_provider: string
  operator_id: number | null
  operator_username: string | null
  source: 'top_up' | 'log'
  amount_known: boolean
  display_amount: string | null
  content: string
  legacy: boolean
}

export interface BalanceSummary {
  paid_money: number | null
  paid_currency: string | null
  paid_orders: number
  paid_totals: {
    currency: string | null
    payment_provider: string
    amount: number
    orders: number
  }[]
  registration_bonus_quota: number
  registration_bonus_quota_exact?: string
  registration_bonus_count: number
  registration_bonus_unknown: number
  registration_coverage?: {
    complete: boolean
    reason: string
    relevant: boolean
  }
  admin_increase_quota: number
  admin_increase_quota_exact?: string
  admin_decrease_quota: number
  admin_decrease_quota_exact?: string
  admin_adjustments: number
  unknown_count: number
  other_unknown: number
  raw_quota_only: boolean
  categories: {
    category: Exclude<BalanceCategory, 'all'>
    count: number
    known_quota_delta: number
    known_quota_delta_exact?: string
    unknown_count: number
  }[]
}

export interface BalanceLedgerResult {
  items: BalanceEntry[]
  total: number
  p: number
  page_size: number
  summary: BalanceSummary
}
