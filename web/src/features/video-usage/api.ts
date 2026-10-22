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
import { isAxiosError } from 'axios'
import { t } from 'i18next'

import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import {
  videoUsageSchema,
  type VideoUsageQuery,
  type VideoUsageResult,
} from './schema'

export class VideoUsageTooBroadError extends Error {
  constructor() {
    super('VIDEO_USAGE_QUERY_TOO_BROAD')
    this.name = 'VideoUsageTooBroadError'
  }
}
export async function getVideoUsage(
  params: VideoUsageQuery
): Promise<VideoUsageResult> {
  const response = await api
    .get<unknown>('/api/video-usage', { params })
    .catch((error: unknown) => {
      if (isAxiosError(error) && error.response?.status === 422) {
        const body: unknown = error.response.data
        if (
          typeof body === 'object' &&
          body !== null &&
          'code' in body &&
          body.code === 'VIDEO_USAGE_QUERY_TOO_BROAD'
        ) {
          throw new VideoUsageTooBroadError()
        }
      }
      throw error
    })
  const envelope = requireServerSuccess(response.data)
  const result = videoUsageSchema.safeParse(
    typeof envelope === 'object' && envelope !== null && 'data' in envelope
      ? envelope.data
      : undefined
  )
  if (!result.success) throw new Error(t('Failed to load video usage'))
  return result.data
}
