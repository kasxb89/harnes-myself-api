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
import type { ModelProfitRow, ProfitTotals } from '../types'

/** Older reports contain billed margins, which are never cash evidence. */
export function verifiedCashAmount(
  totals: ProfitTotals,
  field: 'cash_revenue_cny' | 'gift_consumed_cny' | 'cash_profit_cny'
): number | null {
  if (
    totals.funding_status !== 'attributed' ||
    totals.funding_coverage !== 1 ||
    totals.funding_unknown_events !== 0 ||
    totals.pending_requests !== 0 ||
    totals.pending_billing_events !== 0
  ) {
    return null
  }
  if (
    field === 'cash_profit_cny' &&
    (totals.unknown_cost_events !== 0 ||
      typeof totals.upstream_cost_cny !== 'number' ||
      !Number.isFinite(totals.upstream_cost_cny))
  ) {
    return null
  }
  const value = totals[field]
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

export type ConfirmedFundingField =
  | 'confirmed_cash_revenue_cny'
  | 'confirmed_paid_consumed_cny'
  | 'confirmed_gift_consumed_cny'
  | 'unknown_funding_consumed_cny'

/** Confirmed settled parts can be shown while the whole period remains unknown. */
export function verifiedFundingAmount(
  totals: ProfitTotals,
  field: ConfirmedFundingField
): number | null {
  if (totals.funding_policy !== 'fifo') return null
  const value = totals[field]
  // Refund-only windows can contain negative amounts. Absence is never zero.
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

/** Period averages are observed ledger values, not advertised model tariffs. */
export function verifiedPeriodAverage(
  row: ModelProfitRow,
  field: 'customer_average_price_cny' | 'upstream_average_cost_cny'
): number | null {
  if (
    !Number.isFinite(row.requests) ||
    row.requests <= 0 ||
    row.pending_requests !== 0 ||
    row.pending_billing_events !== 0 ||
    row.average_price_unit !== 'request'
  ) {
    return null
  }
  if (
    field === 'upstream_average_cost_cny' &&
    (row.unknown_cost_events !== 0 || row.cost_coverage !== 1)
  ) {
    return null
  }
  const value = row[field]
  return typeof value === 'number' && Number.isFinite(value) ? value : null
}

/** The report is explicitly CNY; the console's display currency must not relabel it. */
export function formatProfitCny(
  value: number | null | undefined,
  locale?: string
): string {
  if (value == null || !Number.isFinite(value)) return '—'
  const formatter = new Intl.NumberFormat(locale, {
    style: 'currency',
    currency: 'CNY',
    minimumFractionDigits: 2,
    maximumFractionDigits: 8,
  })
  if (value !== 0 && Math.abs(value) < 0.00000001) {
    return `${value < 0 ? '>' : '<'}${formatter.format(value < 0 ? -0.00000001 : 0.00000001)}`
  }
  return formatter.format(value)
}

/** Retain small losses instead of rounding a nonzero margin to negative zero. */
export function formatProfitMargin(
  value: number | null | undefined,
  locale?: string
): string {
  if (value == null || !Number.isFinite(value)) return '—'
  const formatter = new Intl.NumberFormat(locale, {
    style: 'percent',
    maximumFractionDigits: 6,
  })
  if (value !== 0 && Math.abs(value) < 0.00000001) {
    return `${value < 0 ? '>' : '<'}${formatter.format(value < 0 ? -0.00000001 : 0.00000001)}`
  }
  return formatter.format(value)
}
