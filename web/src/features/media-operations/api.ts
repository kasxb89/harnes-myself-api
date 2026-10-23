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
  mediaOperationsSchema,
  type MediaOperationsQuery,
  type MediaOperationsResult,
} from './schema'

export class MediaQueryTooBroadError extends Error {
  constructor() {
    super('MEDIA_OPERATIONS_QUERY_TOO_BROAD')
    this.name = 'MediaQueryTooBroadError'
  }
}

export async function getMediaOperations(
  params: MediaOperationsQuery,
  signal?: AbortSignal
): Promise<MediaOperationsResult> {
  const response = await api
    .get<unknown>('/api/media-operations', {
      params,
      signal,
      disableDuplicate: true,
    })
    .catch((error: unknown) => {
      if (isAxiosError(error) && error.response?.status === 422) {
        const body: unknown = error.response.data
        if (
          typeof body === 'object' &&
          body !== null &&
          'code' in body &&
          body.code === 'MEDIA_OPERATIONS_QUERY_TOO_BROAD'
        ) {
          throw new MediaQueryTooBroadError()
        }
      }
      throw error
    })
  const envelope = requireServerSuccess(response.data)
  const parsed = mediaOperationsSchema.safeParse(
    typeof envelope === 'object' && envelope !== null && 'data' in envelope
      ? envelope.data
      : undefined
  )
  if (!parsed.success) throw new Error(t('Failed to load media operations'))
  return parsed.data
}
