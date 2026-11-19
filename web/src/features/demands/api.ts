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

export const demandStatuses = [
  'pending',
  'communicating',
  'in_progress',
  'awaiting_acceptance',
  'completed',
  'cancelled',
  'closed',
] as const
export const statusLabels = {
  pending: 'Awaiting engineer',
  communicating: 'Discussing',
  in_progress: 'In progress',
  awaiting_acceptance: 'Awaiting acceptance',
  completed: 'Completed',
  cancelled: 'Cancelled',
  closed: 'Closed',
} as const
export const categorySchema = z.object({
  id: z.string().regex(/^[a-z][a-z0-9_-]{0,63}$/),
  name: z.string().min(1).max(100),
  enabled: z.boolean(),
})
export const configSchema = z.object({
  revision: z.number(),
  config: z.object({
    enabled: z.boolean(),
    categories: z.array(categorySchema),
    upload_rules: z.object({
      max_image_bytes: z
        .number()
        .int()
        .min(1)
        .max(50 * 1024 * 1024),
      max_video_bytes: z
        .number()
        .int()
        .min(1)
        .max(2 * 1024 * 1024 * 1024),
      max_attachments: z.number().int().min(1).max(20),
      chunk_bytes: z
        .number()
        .int()
        .min(64 * 1024)
        .max(8 * 1024 * 1024),
      allowed_mime_types: z.array(z.string()).min(1),
      upload_ttl_seconds: z
        .number()
        .int()
        .min(300)
        .max(7 * 86400),
    }),
  }),
})
export const engineerSchema = z.object({
  id: z.string(),
  user_id: z.number().optional(),
  display_name: z.string(),
  avatar_attachment_id: z.string(),
  category_ids: z.array(z.string()),
  introduction: z.string(),
  service_hours: z.string(),
  published: z.boolean(),
  enabled: z.boolean(),
  accepting: z.boolean(),
  capacity: z.number(),
  sort: z.number(),
  revision: z.number(),
  presence: z.enum(['online', 'busy', 'offline']),
  can_start: z.boolean(),
  open_demand_count: z.number(),
})
export const demandSchema = z.object({
  id: z.string(),
  number: z.string(),
  title: z.string(),
  user_id: z.number(),
  engineer_id: z.string(),
  status: z.enum(demandStatuses),
  priority: z.enum(['normal', 'high', 'urgent']),
  revision: z.number(),
  last_seq: z.number(),
  last_read_seq: z.number(),
  unread_count: z.number(),
  preview: z.string(),
  budget: z.string(),
  expected_date: z.string(),
  created_at: z.number(),
  updated_at: z.number(),
  last_message_at: z.number().optional(),
  delivery_id: z.string(),
  engineer: engineerSchema,
  service_profile_id: z.string().optional(),
  service_profile_name: z.string().optional(),
  intake_mode: z.enum(['direct', 'platform']).optional(),
  assigned: z.boolean().optional(),
  owner: z
    .object({
      id: z.number(),
      nickname: z.string(),
      phone: z.string().nullable(),
    })
    .optional(),
})
export const attachmentSchema = z.object({
  id: z.string(),
  name: z.string(),
  kind: z.enum(['image', 'video']),
  mime_type: z.string(),
  size_bytes: z.number(),
  status: z.enum(['uploading', 'assembling', 'ready', 'failed']),
  uploaded_bytes: z.number(),
  sha256: z.string().optional(),
})
export const messageSchema = z.object({
  id: z.string(),
  demand_id: z.string(),
  seq: z.number(),
  sender_id: z.number(),
  author: z.enum(['user', 'engineer', 'system']),
  author_name: z.string().max(200).optional(),
  kind: z.enum(['message', 'delivery', 'system']),
  text: z.string(),
  created_at: z.number(),
  client_message_id: z.string(),
  read: z.boolean(),
  attachments: z.array(attachmentSchema),
})
const listSchema = z.object({
  items: z.array(demandSchema),
  next_before_id: z.string(),
  has_more: z.boolean(),
})
const conversationSchema = z.object({
  items: z.array(messageSchema),
  next_after_seq: z.number(),
  last_seq: z.number(),
  has_more: z.boolean(),
  peer_read_seq: z.number().optional(),
  demand: demandSchema,
})
const auditSchema = z.object({
  items: z.array(
    z.object({
      id: z.number(),
      actor_id: z.number(),
      action: z.string(),
      detail: z.string(),
      created_at: z.number(),
    })
  ),
  next_after_id: z.number(),
  has_more: z.boolean(),
})
const uploadSchema = z.object({
  attachment: attachmentSchema,
  offset: z.number(),
  chunk_bytes: z.number(),
  expires_at: z.number(),
})
const linkSchema = z.object({ url: z.string().url(), expires_at: z.number() })
export type Demand = z.infer<typeof demandSchema>
export type Engineer = z.infer<typeof engineerSchema>
export type DemandConfig = z.infer<typeof configSchema>
export type Message = z.infer<typeof messageSchema>
export type Attachment = z.infer<typeof attachmentSchema>
export type DemandMode = 'admin' | 'engineer'
type Envelope = { success: boolean; data: unknown; message?: string }
export const demandBase = (mode: DemandMode) =>
  mode === 'admin' ? '/api/demand-management' : '/api/engineer/demands'
// React Query owns read deduplication; each observer gets a fresh HTTP abort lifecycle.
export async function loadDemands(
  mode: DemandMode,
  filters: Record<string, string>,
  signal?: AbortSignal
) {
  const r = await api.get<Envelope>(demandBase(mode), {
    params: filters,
    signal,
    disableDuplicate: true,
  })
  return listSchema.parse(requireServerSuccess(r.data).data)
}
export async function loadConversation(
  mode: DemandMode,
  id: string,
  after: number,
  signal?: AbortSignal
) {
  const r = await api.get<Envelope>(
    `${demandBase(mode)}/${encodeURIComponent(id)}/messages`,
    { params: { after_seq: after, limit: 100 }, signal, disableDuplicate: true }
  )
  return conversationSchema.parse(requireServerSuccess(r.data).data)
}
export async function loadEngineers(signal?: AbortSignal) {
  const r = await api.get<Envelope>('/api/demand-management/engineers', {
    signal,
    disableDuplicate: true,
  })
  return z
    .object({ items: z.array(engineerSchema) })
    .parse(requireServerSuccess(r.data).data).items
}
export async function saveEngineer(value: Engineer) {
  const base = '/api/demand-management/engineers'
  const r = value.id
    ? await api.put<Envelope>(`${base}/${encodeURIComponent(value.id)}`, value)
    : await api.post<Envelope>(base, value)
  return engineerSchema.parse(requireServerSuccess(r.data).data)
}
export async function loadDemandConfig(signal?: AbortSignal) {
  const r = await api.get<Envelope>('/api/demand-management/config', {
    signal,
    disableDuplicate: true,
  })
  return configSchema.parse(requireServerSuccess(r.data).data)
}
export async function saveDemandConfig(value: DemandConfig) {
  const r = await api.put<Envelope>(
    '/api/demand-management/config',
    configSchema.parse(value)
  )
  return configSchema.parse(requireServerSuccess(r.data).data)
}
export async function demandAction(
  mode: DemandMode,
  id: string,
  body: Record<string, unknown>
) {
  const r = await api.post<Envelope>(
    `${demandBase(mode)}/${encodeURIComponent(id)}/actions`,
    body
  )
  return z
    .object({ demand: demandSchema, replayed: z.boolean() })
    .parse(requireServerSuccess(r.data).data)
}
export async function sendDemandMessage(
  mode: DemandMode,
  id: string,
  body: { client_message_id: string; text: string; attachment_ids: string[] }
) {
  const r = await api.post<Envelope>(
    `${demandBase(mode)}/${encodeURIComponent(id)}/messages`,
    body
  )
  return z
    .object({
      demand: demandSchema,
      message: messageSchema,
      replayed: z.boolean(),
    })
    .parse(requireServerSuccess(r.data).data)
}
export async function readDemand(
  mode: DemandMode,
  id: string,
  last_read_seq: number
) {
  const r = await api.post<Envelope>(
    `${demandBase(mode)}/${encodeURIComponent(id)}/read`,
    { last_read_seq }
  )
  requireServerSuccess(r.data)
}
export async function heartbeatEngineer(accepting?: boolean) {
  const r = await api.post<Envelope>(
    '/api/engineer/demands/heartbeat',
    accepting === undefined ? {} : { accepting }
  )
  return z
    .object({
      presence: z.enum(['online', 'busy', 'offline']),
      server_time: z.number(),
      engineer: engineerSchema,
    })
    .parse(requireServerSuccess(r.data).data)
}
export async function loadDemandAudit(
  id: string,
  signal?: AbortSignal,
  after = 0
) {
  const r = await api.get<Envelope>(
    `/api/demand-management/${encodeURIComponent(id)}/audit`,
    { signal, params: { after_id: after }, disableDuplicate: true }
  )
  return auditSchema.parse(requireServerSuccess(r.data).data)
}
export async function attachmentLink(
  id: string,
  disposition: 'inline' | 'attachment',
  mode: DemandMode = 'engineer'
) {
  const base =
    mode === 'admin' ? '/api/demand-management' : '/api/product/demands'
  const r = await api.post<Envelope>(
    `${base}/attachments/${encodeURIComponent(id)}/link`,
    { disposition }
  )
  return linkSchema.parse(requireServerSuccess(r.data).data)
}
/** Retry the same upload session, recovering the committed offset before resuming. */
export async function uploadDemandFile(
  file: File,
  key: string,
  onProgress: (value: number) => void,
  mode: DemandMode = 'engineer',
  signal?: AbortSignal
) {
  const base =
    mode === 'admin' ? '/api/demand-management' : '/api/product/demands'
  const r = await api.post<Envelope>(
    `${base}/uploads`,
    {
      client_upload_id: key,
      name: file.name,
      mime_type: file.type,
      size_bytes: file.size,
    },
    { signal, disableDuplicate: true }
  )
  let current = uploadSchema.parse(requireServerSuccess(r.data).data)
  if (current.attachment.status === 'ready') return current.attachment
  while (current.offset < file.size) {
    const offset = current.offset
    const chunk = file.slice(
      offset,
      Math.min(file.size, offset + current.chunk_bytes)
    )
    const part = await api.put<Envelope>(
      `${base}/uploads/${encodeURIComponent(current.attachment.id)}/chunks`,
      chunk,
      {
        signal,
        headers: {
          'Content-Type': 'application/octet-stream',
          'Upload-Offset': String(offset),
        },
      }
    )
    current = uploadSchema.parse(requireServerSuccess(part.data).data)
    if (current.offset <= offset) {
      throw new Error('Upload progress did not advance')
    }
    onProgress(Math.floor((current.offset / file.size) * 100))
  }
  const done = await api.post<Envelope>(
    `${base}/uploads/${encodeURIComponent(current.attachment.id)}/complete`,
    {},
    { signal, disableDuplicate: true }
  )
  return uploadSchema.parse(requireServerSuccess(done.data).data).attachment
}

export async function loadUploadRules(
  signal?: AbortSignal,
  mode: DemandMode = 'engineer'
) {
  const r = await api.get<Envelope>(
    mode === 'admin'
      ? '/api/demand-management/config'
      : '/api/product/demands/config',
    {
      signal,
      disableDuplicate: true,
    }
  )
  return configSchema.shape.config.parse(
    z.object({ config: z.unknown() }).parse(requireServerSuccess(r.data).data)
      .config
  ).upload_rules
}
