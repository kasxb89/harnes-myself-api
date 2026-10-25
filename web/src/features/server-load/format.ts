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
import { formatNumber } from '@/lib/format'

export function formatServerBytes(
  bytes: number | null,
  locale: string | undefined,
  unknown: string
): string {
  if (bytes === null) return unknown
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB']
  const index =
    bytes > 0
      ? Math.min(Math.floor(Math.log(bytes) / Math.log(1024)), units.length - 1)
      : 0
  return `${formatNumber(bytes / 1024 ** index, locale)} ${units[index]}`
}

export function formatServerPercent(
  value: number | null,
  locale: string | undefined,
  unknown: string
): string {
  return value === null ? unknown : `${formatNumber(value, locale)}%`
}
