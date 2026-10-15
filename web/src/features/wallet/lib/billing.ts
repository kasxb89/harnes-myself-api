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
import type { StatusBadgeProps } from '@/components/status-badge'
import {
  formatCurrencyFromUSD,
  formatQuotaWithCurrency,
  type CurrencyFormatOptions,
} from '@/lib/currency'
import { formatTimestampToDate } from '@/lib/format'

import type { TopupRecord, TopupStatus } from '../types'

// ============================================================================
// Billing Utility Functions
// ============================================================================

interface StatusConfig {
  variant: StatusBadgeProps['variant']
  label: string
}

/**
 * Status badge configuration
 */
export const STATUS_CONFIG: Record<TopupStatus, StatusConfig> = {
  success: {
    variant: 'success',
    label: 'Success',
  },
  pending: {
    variant: 'warning',
    label: 'Pending',
  },
  expired: {
    variant: 'danger',
    label: 'Expired',
  },
}

/**
 * Get status badge configuration
 */
export function getStatusConfig(status: TopupStatus): StatusConfig {
  return STATUS_CONFIG[status] || STATUS_CONFIG.pending
}

/**
 * Payment method display names
 */
export const PAYMENT_METHOD_NAMES: Record<string, string> = {
  stripe: 'Stripe',
  alipay: 'Alipay',
  wxpay: 'WeChat Pay',
  waffo: 'Waffo',
}

/**
 * Get payment method display name
 */
export function getPaymentMethodName(
  method: string,
  t?: (key: string) => string
): string {
  const name = PAYMENT_METHOD_NAMES[method] || method
  return t ? t(name) : name
}

/**
 * Format timestamp to readable date string
 */
export function formatTimestamp(timestamp: number): string {
  return formatTimestampToDate(timestamp)
}

/**
 * Providers that write the credited quota into `amount` at order creation.
 *
 * Every other provider writes a USD figure there and multiplies it by
 * QuotaPerUnit when the order is credited, so a record's `amount` cannot be
 * turned into money without knowing which group it belongs to.
 */
const QUOTA_AMOUNT_PROVIDERS = new Set(['creem', 'wechat'])

/**
 * Format a top-up record's `amount` as money, in the unit its provider uses.
 *
 * Reading every record as USD inflates the credit-amount column of a quota-unit
 * provider by QuotaPerUnit, so the two groups are formatted differently.
 */
export function formatTopUpAmount(
  record: Pick<TopupRecord, 'amount' | 'payment_provider'>,
  options?: CurrencyFormatOptions
): string {
  const isQuota = QUOTA_AMOUNT_PROVIDERS.has(record.payment_provider ?? '')
  return isQuota
    ? formatQuotaWithCurrency(record.amount, options)
    : formatCurrencyFromUSD(record.amount, options)
}
