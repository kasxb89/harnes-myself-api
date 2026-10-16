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
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import {
  contentSchema,
  type CreationAsset,
  type CreationContent,
} from './lib/schema'

type Response<T> = { success: boolean; data: T; message?: string }
export async function loadContent(): Promise<CreationContent> {
  const response = await api.get<Response<CreationContent>>(
    '/api/creation/admin/content'
  )
  return contentSchema.parse(requireServerSuccess(response.data).data)
}
export async function saveContent(
  content: CreationContent
): Promise<CreationContent> {
  const response = await api.put<Response<CreationContent>>(
    '/api/creation/admin/content',
    contentSchema.parse(content)
  )
  return contentSchema.parse(requireServerSuccess(response.data).data)
}
export async function uploadAsset(file: File): Promise<CreationAsset> {
  const body = new FormData()
  body.append('file', file)
  const response = await api.post<Response<CreationAsset>>(
    '/api/creation/admin/assets',
    body
  )
  return requireServerSuccess(response.data).data
}
