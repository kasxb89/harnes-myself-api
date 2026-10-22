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
import type { VideoUsageQuery } from '../schema'

export interface VideoUsageDraft {
  start?: Date
  end?: Date
  userId: string
  model: string
  source: 'all' | 'desktop' | 'unknown'
}
export function initialVideoFilters(): VideoUsageDraft {
  const now = Date.now()
  return {
    start: new Date(now - 86400000),
    end: new Date(now),
    userId: '',
    model: '',
    source: 'all',
  }
}
export function videoQueryFromDraft(
  draft: VideoUsageDraft
): VideoUsageQuery | null {
  const start = draft.start?.getTime()
  const end = draft.end?.getTime()
  if (
    start === undefined ||
    end === undefined ||
    !Number.isFinite(start) ||
    !Number.isFinite(end) ||
    start < 0 ||
    end <= start ||
    end > Date.now() ||
    end - start > 31 * 86400000
  ) {
    return null
  }
  const userId = draft.userId ? Number(draft.userId) : undefined
  if (userId !== undefined && (!Number.isSafeInteger(userId) || userId <= 0)) {
    return null
  }
  return {
    start_timestamp: Math.floor(start / 1000),
    end_timestamp: Math.floor(end / 1000),
    client_source: draft.source,
    ...(draft.model ? { model: draft.model } : {}),
    ...(userId !== undefined ? { user_id: userId } : {}),
    task_scope: 'history',
    p: 1,
    page_size: 20,
  }
}
