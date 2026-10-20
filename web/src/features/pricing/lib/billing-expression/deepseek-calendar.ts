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
import calendar from '../../../../../../pkg/billingexpr/deepseek_calendar.json'
import { BillingExpressionError } from './types'

const formatter = new Intl.DateTimeFormat('en-US', {
  timeZone: calendar.timezone,
  calendar: 'gregory',
  numberingSystem: 'latn',
  hourCycle: 'h23',
  year: 'numeric',
  month: '2-digit',
  day: '2-digit',
  hour: 'numeric',
  weekday: 'short',
})

export const DEEPSEEK_CALENDAR_YEARS = Object.keys(calendar.years).join(', ')

/** Uses the same versioned public-holiday data as the server. */
export function isDeepSeekPeak(now: Date): boolean {
  if (!Number.isFinite(now.getTime())) {
    throw new BillingExpressionError({
      code: 'type',
      detail: 'simulation time',
    })
  }
  const parts = Object.fromEntries(
    formatter.formatToParts(now).map((part) => [part.type, part.value])
  )
  const holidays = (
    calendar.years as Record<string, { start: string; end: string }[]>
  )[parts.year]
  // Never assume a future year has no holidays or charge peak rates without a calendar.
  if (!holidays || parts.weekday === 'Sat' || parts.weekday === 'Sun') {
    return false
  }
  const date = `${parts.year}-${parts.month}-${parts.day}`
  if (holidays.some(({ start, end }) => date >= start && date <= end)) {
    return false
  }
  const hour = Number(parts.hour)
  return (hour >= 9 && hour < 12) || (hour >= 14 && hour < 18)
}
