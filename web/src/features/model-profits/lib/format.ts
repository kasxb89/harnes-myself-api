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
