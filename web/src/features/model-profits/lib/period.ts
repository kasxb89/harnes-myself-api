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
import type { ProfitFilters } from '../types'

export interface ProfitPeriod {
  start: string
  end: string
  model: string
}

export type ProfitPreset = 'today' | 'yesterday' | 'week' | 'month'

const DAY_SECONDS = 86400

export function shanghaiDateTime(now = Date.now()): string {
  return new Date(now + 8 * 3600000).toISOString().slice(0, 16)
}

export function profitPreset(
  preset: ProfitPreset,
  now = Date.now()
): ProfitPeriod {
  const today = shanghaiDateTime(now).slice(0, 10)
  const midnight = Date.parse(`${today}T00:00:00+08:00`)
  if (preset === 'yesterday') {
    const yesterday = shanghaiDateTime(midnight - DAY_SECONDS * 1000).slice(
      0,
      10
    )
    return { start: yesterday, end: yesterday, model: '' }
  }
  if (preset === 'week') {
    return {
      start: shanghaiDateTime(midnight - 6 * DAY_SECONDS * 1000).slice(0, 10),
      end: today,
      model: '',
    }
  }
  if (preset === 'month') {
    return { start: `${today.slice(0, 7)}-01`, end: today, model: '' }
  }
  return { start: today, end: today, model: '' }
}

export function parseProfitPeriod(period: ProfitPeriod): ProfitFilters | null {
  const validDay = (value: string): boolean => {
    if (!/^\d{4}-\d{2}-\d{2}$/.test(value)) return false
    const timestamp = Date.parse(`${value}T00:00:00+08:00`)
    return (
      Number.isFinite(timestamp) &&
      shanghaiDateTime(timestamp).startsWith(value)
    )
  }
  if (!validDay(period.start) || !validDay(period.end)) return null
  const start = Date.parse(`${period.start}T00:00:00+08:00`) / 1000
  const end = Date.parse(`${period.end}T00:00:00+08:00`) / 1000 + DAY_SECONDS
  if (start >= end || start < 0) return null
  return {
    start_timestamp: start,
    end_timestamp: end,
    model_name: period.model.trim() || undefined,
  }
}
