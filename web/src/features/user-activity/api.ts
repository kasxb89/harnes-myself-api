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
import { t } from 'i18next'

import type { ApiResponse } from '@/features/profile/types'
import { api } from '@/lib/api'
import { createServerError } from '@/lib/server-error-message'

import type {
  ActivityFilters,
  ClientActivityDetail,
  ClientActivitySession,
  ClientActivitySummary,
} from './types'

async function requestActivity<T>(
  path: string,
  params?: object,
  signal?: AbortSignal
): Promise<T> {
  const response = await api.get<ApiResponse<T>>(path, {
    params,
    signal,
    disableDuplicate: true,
  })
  if (!response.data.success || !response.data.data) {
    throw createServerError(response.data, t('Failed to load client activity'))
  }
  return response.data.data
}
export function getActivitySessions(
  params: ActivityFilters,
  signal?: AbortSignal
): Promise<{ items: ClientActivitySession[]; total: number }> {
  if (params.scope === 'live') {
    const {
      start_timestamp: _start,
      end_timestamp: _end,
      time_basis: _basis,
      ...live
    } = params
    return requestActivity('/api/client-activity/sessions', live, signal)
  }
  return requestActivity('/api/client-activity/sessions', params, signal)
}
export function getActivitySummary(
  params: Pick<ActivityFilters, 'start_timestamp' | 'end_timestamp'>,
  signal?: AbortSignal
): Promise<ClientActivitySummary> {
  return requestActivity('/api/client-activity/summary', params, signal)
}
export function getActivityDetail(
  id: number,
  signal?: AbortSignal
): Promise<ClientActivityDetail> {
  return requestActivity(
    `/api/client-activity/sessions/${id}`,
    undefined,
    signal
  )
}
