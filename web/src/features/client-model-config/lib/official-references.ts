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
import { dateInputValue } from '@/components/date-time-utils'

import type { OfficialReferenceRow } from '../unified-api'

export function referenceSpecificationKey(row: OfficialReferenceRow): string {
  return JSON.stringify([
    Object.entries(row.params).sort(([a], [b]) => a.localeCompare(b)),
    row.direction ?? '',
    row.unit,
  ])
}

export function newOfficialReference(
  specification: OfficialReferenceRow,
  model: string
): OfficialReferenceRow {
  return {
    ...specification,
    params: { ...specification.params },
    official_reference: specification.official_reference
      ? { ...specification.official_reference }
      : {
          schema_version: 1,
          currency: 'CNY',
          official_cny: '',
          model,
          unit: specification.unit,
          source_url: '',
          checked_at: '',
          conditions: '',
          source_currency: 'CNY',
        },
  }
}

export function validReferenceAmount(value: string): boolean {
  return (
    /^(0|[1-9][0-9]{0,17})(\.[0-9]{1,24})?$/.test(value) && Number(value) > 0
  )
}

export function officialReferenceValid(row: OfficialReferenceRow): boolean {
  const ref = row.official_reference
  if (
    !ref ||
    !validReferenceAmount(ref.official_cny) ||
    !ref.model.trim() ||
    ref.model.trim() !== ref.model ||
    ref.conditions.trim() !== ref.conditions ||
    ref.model.length > 191 ||
    ref.unit !== row.unit ||
    ref.conditions.length > 4096 ||
    [...(ref.model + ref.conditions + ref.source_url)].some((character) => {
      const code = character.charCodeAt(0)
      return code < 32 || code === 127
    }) ||
    !/^\d{4}-\d{2}-\d{2}$/.test(ref.checked_at)
  ) {
    return false
  }
  const date = new Date(`${ref.checked_at}T00:00:00`)
  if (
    !Number.isFinite(date.getTime()) ||
    dateInputValue(date) !== ref.checked_at ||
    ref.checked_at > dateInputValue(new Date()) ||
    (ref.source_currency === 'USD' &&
      !validReferenceAmount(ref.usd_cny ?? '')) ||
    (ref.usd_cny !== undefined &&
      ref.usd_cny !== '' &&
      !validReferenceAmount(ref.usd_cny)) ||
    (ref.official_total_cny !== undefined &&
      ref.official_total_cny !== '' &&
      !validReferenceAmount(ref.official_total_cny))
  ) {
    return false
  }
  try {
    const url = new URL(ref.source_url)
    return (
      url.protocol === 'https:' &&
      !url.username &&
      !url.password &&
      !!url.hostname &&
      ref.source_url.length <= 2048
    )
  } catch {
    return false
  }
}
