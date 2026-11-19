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
import { z } from 'zod'

import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

const author = z.object({
  id: z.number().int().positive(),
  display_name: z.string(),
})
export const contentSchema = z.object({
  id: z.string(),
  title: z.string().optional(),
  body: z.string(),
  author,
  post_id: z.string().optional(),
  post_title: z.string().optional(),
  created_at: z.number(),
  reviewed_at: z.number().optional(),
  deleted_at: z.number().optional(),
  delete_reason: z.string().optional(),
  like_count: z.number().optional(),
  comment_count: z.number().optional(),
})
export type ForumContent = z.infer<typeof contentSchema>
export type ContentKind = 'posts' | 'comments'
export const configSchema = z.object({
  revision: z.number(),
  enabled: z.boolean(),
})
const base = '/api/forum-management'
export async function loadContent(
  kind: ContentKind,
  review: string,
  q: string,
  cursor: string,
  signal?: AbortSignal
) {
  const r = await api.get(`${base}/${kind}`, {
    params: { review, q, cursor, limit: 30 },
    signal,
    disableDuplicate: true,
  })
  return z
    .object({
      items: z.array(contentSchema),
      has_more: z.boolean(),
      next_cursor: z.string(),
    })
    .parse(requireServerSuccess(r.data).data)
}
export async function moderate(
  kind: ContentKind,
  id: string,
  action: 'review' | 'delete',
  reason: string
) {
  const path = `${base}/${kind}/${encodeURIComponent(id)}`
  const r =
    action === 'review'
      ? await api.put(`${path}/review`, { reviewed: true })
      : await api.delete(path, { data: { reason } })
  requireServerSuccess(r.data)
}
export async function loadConfig(signal?: AbortSignal) {
  const r = await api.get(`${base}/config`, { signal, disableDuplicate: true })
  return configSchema.parse(requireServerSuccess(r.data).data)
}
export async function saveConfig(value: z.infer<typeof configSchema>) {
  const r = await api.put(`${base}/config`, value)
  return configSchema.parse(requireServerSuccess(r.data).data)
}
