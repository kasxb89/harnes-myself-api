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
import type { MediaFiltersDraft } from '../components/filters'
import type { MediaOperationsQuery } from '../schema'

export function initialMediaFilters(): MediaFiltersDraft {
  const end = Date.now()
  return {
    start: new Date(end - 86400000),
    end: new Date(end),
    kind: 'all',
    model: '',
  }
}

export function mediaQueryFromDraft(
  draft: MediaFiltersDraft
): MediaOperationsQuery | null {
  const start = draft.start?.getTime()
  const end = draft.end?.getTime()
  if (
    start === undefined ||
    end === undefined ||
    !Number.isFinite(start) ||
    !Number.isFinite(end) ||
    start > end ||
    end - start > 31 * 86400000 ||
    start < 0 ||
    end > Date.now()
  ) {
    return null
  }
  return {
    start_timestamp: Math.floor(start / 1000),
    end_timestamp: Math.floor(end / 1000),
    media_kind: draft.kind,
    ...(draft.model.trim() ? { model: draft.model.trim() } : {}),
  }
}
