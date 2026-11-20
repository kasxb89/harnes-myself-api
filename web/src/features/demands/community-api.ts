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

import { configSchema } from './api'

export const communityStatuses = [
  'open',
  'accepted',
  'awaiting_acceptance',
  'completed',
  'cancelled',
] as const
export const communityStatusLabels = {
  open: 'Open for acceptance',
  accepted: 'Accepted by user',
  awaiting_acceptance: 'Awaiting acceptance',
  completed: 'Completed',
  cancelled: 'Cancelled',
} as const
export const communityApprovalStatuses = [
  'pending',
  'approved',
  'rejected',
] as const
export const communityApprovalLabels = {
  pending: 'Pending approval',
  approved: 'Approved listing',
  rejected: 'Rejected listing',
} as const
export const communityModerationActions = [
  'hide_listing',
  'show_listing',
  'cancel',
  'reopen',
  'approve_listing',
  'reject_listing',
] as const
const person = z.object({
  id: z.number().int().positive(),
  display_name: z.string(),
  phone_e164: z.string().nullish(),
  status: z.number().optional(),
})
export const communityAttachmentSchema = z.object({
  id: z.string(),
  name: z.string(),
  kind: z.enum(['image', 'video']),
  mime_type: z.string(),
  size_bytes: z.number(),
  status: z.literal('ready'),
  scope: z.enum(['listing', 'conversation']),
  sha256: z.string().optional(),
})
export const communityOrderSchema = z.object({
  id: z.string(),
  number: z.string(),
  title: z.string(),
  status: z.enum(communityStatuses),
  revision: z.number(),
  listing_visible: z.boolean(),
  publisher: person,
  acceptor: person.nullable(),
  my_role: z.enum(['publisher', 'acceptor', 'admin']),
  listing: z.object({
    public_summary: z.string(),
    public_budget: z.string(),
    expected_delivery_date: z.string().optional(),
    approval_status: z.enum(communityApprovalStatuses).optional(),
    approval_reason: z.string().optional(),
    service_profile_name: z.string(),
    public_attachments: z.array(communityAttachmentSchema),
  }),
  private_text: z.string(),
  last_seq: z.number(),
  last_read_seq: z.number(),
  unread_count: z.number(),
  created_at: z.number(),
  updated_at: z.number(),
  can_send: z.boolean(),
  can_cancel: z.boolean(),
  can_reopen: z.boolean(),
})
export const communityMessageSchema = z.object({
  id: z.string(),
  order_id: z.string(),
  seq: z.number(),
  sender_id: z.number(),
  author_kind: z.enum(['publisher', 'acceptor', 'admin', 'system']),
  author_name: z.string(),
  kind: z.enum(['message', 'delivery', 'system']),
  text: z.string(),
  attachments: z.array(communityAttachmentSchema),
  created_at: z.number(),
  client_message_id: z.string(),
  read: z.boolean(),
})
export type CommunityOrder = z.infer<typeof communityOrderSchema>
export type CommunityMessage = z.infer<typeof communityMessageSchema>
export type CommunityAttachment = z.infer<typeof communityAttachmentSchema>
export const communityConfigSchema = z.object({
  revision: z.number(),
  enabled: z.boolean(),
  capabilities: z
    .object({ community_approval_v1: z.boolean().optional() })
    .optional(),
  publication_policy: z.literal('approval_required').optional(),
  upload_rules: configSchema.shape.config.shape.upload_rules,
})
const base = '/api/community-demand-management'
const orderPath = (id: string) => `${base}/orders/${encodeURIComponent(id)}`
export async function loadCommunityConfig(signal?: AbortSignal) {
  const r = await api.get(`${base}/config`, { signal, disableDuplicate: true })
  return communityConfigSchema.parse(requireServerSuccess(r.data).data)
}
export async function saveCommunityConfig(body: {
  revision: number
  enabled: boolean
}) {
  const r = await api.put(`${base}/config`, body)
  return z
    .object({ revision: z.number(), enabled: z.boolean() })
    .parse(requireServerSuccess(r.data).data)
}
export async function loadCommunityOrders(
  params: {
    status: string
    visibility: string
    approval_status: string
    publisher_id: string
    acceptor_id: string
    q: string
    cursor: string
  },
  signal?: AbortSignal
) {
  const r = await api.get(`${base}/orders`, {
    signal,
    params: { ...params, limit: 30 },
    disableDuplicate: true,
  })
  return z
    .object({
      items: z.array(communityOrderSchema),
      next_cursor: z.string(),
      has_more: z.boolean(),
    })
    .parse(requireServerSuccess(r.data).data)
}
export async function loadCommunityOrder(id: string, signal?: AbortSignal) {
  const r = await api.get(orderPath(id), { signal, disableDuplicate: true })
  const order = z
    .object({ order: communityOrderSchema })
    .parse(requireServerSuccess(r.data).data).order
  if (order.id !== id) throw new Error('Order identifier mismatch')
  return order
}
export async function loadCommunityConversation(
  id: string,
  after: number,
  signal?: AbortSignal
) {
  const r = await api.get(`${orderPath(id)}/messages`, {
    params: { after_seq: after, limit: 100 },
    signal,
    disableDuplicate: true,
  })
  return z
    .object({
      items: z.array(communityMessageSchema),
      order: communityOrderSchema,
      last_seq: z.number(),
      next_after_seq: z.number(),
      has_more: z.boolean(),
    })
    .parse(requireServerSuccess(r.data).data)
}
const receipt = z.object({
  order: communityOrderSchema,
  message: communityMessageSchema.nullable(),
  replayed: z.boolean(),
})
export type CommunityOutbox = {
  client_message_id: string
  text: string
  attachment_ids: string[]
}
export async function sendCommunityMessage(id: string, body: CommunityOutbox) {
  const r = await api.post(`${orderPath(id)}/messages`, body, {
    disableDuplicate: true,
  })
  return receipt.parse(requireServerSuccess(r.data).data)
}
export type CommunityModeration = {
  client_action_id: string
  revision: number
  action: (typeof communityModerationActions)[number]
  reason: string
}
export async function moderateCommunity(id: string, body: CommunityModeration) {
  const r = await api.post(`${orderPath(id)}/actions`, body, {
    disableDuplicate: true,
  })
  return receipt.parse(requireServerSuccess(r.data).data)
}
export async function loadCommunityAudit(id: string, signal?: AbortSignal) {
  const r = await api.get(`${orderPath(id)}/audit`, {
    signal,
    disableDuplicate: true,
  })
  return z
    .object({
      items: z.array(
        z.object({
          id: z.number(),
          actor_id: z.number(),
          action: z.string(),
          reason: z.string(),
          created_at: z.number(),
        })
      ),
      has_more: z.boolean(),
    })
    .parse(requireServerSuccess(r.data).data)
}
export async function communityAttachmentLink(
  id: string,
  disposition: 'inline' | 'attachment'
) {
  const r = await api.post(
    `${base}/attachments/${encodeURIComponent(id)}/link`,
    { disposition }
  )
  return z
    .object({ url: z.string(), expires_at: z.number() })
    .parse(requireServerSuccess(r.data).data)
}
const upload = z.object({
  id: z.string(),
  offset: z.number(),
  chunk_bytes: z.number(),
  status: z.enum(['uploading', 'ready']),
  attachment: communityAttachmentSchema.nullable(),
})
/** Explicit retry reuses the same upload key and reads its committed offset first. */
export async function uploadCommunityFile(
  orderId: string,
  file: File,
  key: string,
  signal?: AbortSignal
) {
  const options = { signal, disableDuplicate: true }
  const r = await api.post(
    `${base}/uploads`,
    {
      client_upload_id: key,
      order_id: orderId,
      scope: 'conversation',
      name: file.name,
      mime_type: file.type,
      size_bytes: file.size,
    },
    options
  )
  let current = upload.parse(requireServerSuccess(r.data).data)
  if (current.attachment) return current.attachment
  const state = await api.get(`${base}/uploads/${current.id}`, options)
  current = upload.parse(requireServerSuccess(state.data).data)
  if (current.attachment) return current.attachment
  while (current.offset < file.size) {
    const before = current.offset
    const chunk = file.slice(
      before,
      Math.min(file.size, before + current.chunk_bytes)
    )
    const part = await api.put(`${base}/uploads/${current.id}/content`, chunk, {
      ...options,
      headers: {
        'Content-Type': 'application/octet-stream',
        'Upload-Offset': String(before),
      },
    })
    current = upload.parse(requireServerSuccess(part.data).data)
    if (current.offset <= before) {
      throw new Error('Upload progress did not advance')
    }
  }
  const hash = await crypto.subtle.digest('SHA-256', await file.arrayBuffer())
  const sha256 = Array.from(new Uint8Array(hash), (n) =>
    n.toString(16).padStart(2, '0')
  ).join('')
  const done = await api.post(
    `${base}/uploads/${current.id}/complete`,
    { sha256 },
    options
  )
  const attachment = upload.parse(
    requireServerSuccess(done.data).data
  ).attachment
  if (!attachment) throw new Error('Attachment is not ready')
  return attachment
}

export async function readCommunityOrder(id: string, seq: number) {
  const r = await api.post(
    `${orderPath(id)}/read`,
    { last_read_seq: seq },
    { disableDuplicate: true }
  )
  requireServerSuccess(r.data)
}
