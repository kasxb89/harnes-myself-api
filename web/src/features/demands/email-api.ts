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

const base = '/api/demand-management/email-notifications'
export const emailTypes = [
  'official_created',
  'community_pending',
  'customer_message',
  'test',
] as const
export const deliveryStatuses = [
  'pending',
  'leased',
  'sending',
  'sent',
  'failed',
  'unknown',
  'paused',
] as const
export const emailTypeLabels = {
  official_created: 'New official customization',
  community_pending: 'New community approval request',
  customer_message: 'New customer messages',
  test: 'Test email',
} as const
export const deliveryStatusLabels = {
  pending: 'Email queued',
  leased: 'Email preparing',
  sending: 'Email sending',
  sent: 'SMTP accepted',
  failed: 'Email failed',
  unknown: 'Delivery unknown',
  paused: 'Email paused',
} as const
export function validAdminBaseURL(value: string) {
  if (/[?#]/.test(value)) return false
  try {
    const url = new URL(value)
    return (
      (url.protocol === 'https:' ||
        (url.protocol === 'http:' &&
          ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname))) &&
      !url.username &&
      !url.password &&
      !url.search &&
      !url.hash
    )
  } catch {
    return false
  }
}
export function normalizeRecipients(value: string) {
  return [
    ...new Set(
      value
        .split(/[\n,;]+/)
        .map((v) => v.trim().toLowerCase())
        .filter(Boolean)
    ),
  ]
}
export const emailConfigSchema = z.object({
  revision: z.number().int().nonnegative(),
  enabled: z.boolean(),
  recipients: z.array(z.email().max(254)).max(20),
  types: z.object({
    official_created: z.boolean(),
    community_pending: z.boolean(),
    customer_message: z.boolean(),
  }),
  message_merge_seconds: z.number().int().min(30).max(3600),
  admin_base_url: z.string().max(2048),
})
const configReply = z.object({
  config: emailConfigSchema,
  smtp_ready: z.boolean(),
})
export type EmailConfig = z.infer<typeof emailConfigSchema>
export type EmailConfigReply = z.infer<typeof configReply>
export const emailDeliverySchema = z.object({
  id: z.string(),
  domain: z.enum(['official', 'community', 'test']),
  entity_id: z.string(),
  type: z.enum(emailTypes),
  recipient: z.string(),
  status: z.enum(deliveryStatuses),
  number: z.string(),
  title: z.string(),
  event_count: z.number().int().nonnegative(),
  attempt_count: z.number().int().nonnegative(),
  due_at: z.number(),
  created_at: z.number(),
  updated_at: z.number(),
  sent_at: z.number(),
  error_code: z.string(),
  error_stage: z.string(),
  pause_reason: z.string(),
  detail_url: z.string(),
})
export type EmailDelivery = z.infer<typeof emailDeliverySchema>
const listSchema = z.object({
  items: z.array(emailDeliverySchema),
  next_cursor: z.string(),
  has_more: z.boolean(),
})
const receiptSchema = z.object({
  event_id: z.string(),
  delivery_ids: z.array(z.string()),
  replayed: z.boolean(),
})
export const emailTestActionSchema = z.object({
  client_action_id: z.uuid(),
  revision: z.number().int().nonnegative(),
})
export type EmailTestAction = z.infer<typeof emailTestActionSchema>
export async function loadEmailConfig(signal?: AbortSignal) {
  const r = await api.get(`${base}/config`, { signal, disableDuplicate: true })
  return configReply.parse(requireServerSuccess(r.data).data)
}
export async function saveEmailConfig(body: EmailConfig) {
  const r = await api.put(`${base}/config`, body)
  return configReply.parse(requireServerSuccess(r.data).data)
}
export async function loadEmailDeliveries(
  params: { cursor: string; status: string; type: string },
  signal?: AbortSignal
) {
  const r = await api.get(`${base}/deliveries`, {
    params: { ...params, limit: 30 },
    signal,
    disableDuplicate: true,
  })
  return listSchema.parse(requireServerSuccess(r.data).data)
}
export async function sendTestEmail(body: EmailTestAction) {
  const r = await api.post(`${base}/test`, body, { disableDuplicate: true })
  return receiptSchema.parse(requireServerSuccess(r.data).data)
}
export async function loadEmailTestReceipt(action: EmailTestAction) {
  const r = await api.get(
    `${base}/test-receipts/${encodeURIComponent(action.client_action_id)}`,
    { disableDuplicate: true }
  )
  return receiptSchema.parse(requireServerSuccess(r.data).data)
}
/** Only link a delivery to its exact domain/entity, never trust an arbitrary URL. */
export function emailDetailURL(delivery: EmailDelivery) {
  if (delivery.domain === 'test' || !delivery.entity_id) return undefined
  try {
    const url = new URL(delivery.detail_url)
    const tab = delivery.domain === 'official' ? 'demands' : 'community'
    const idKey = delivery.domain === 'official' ? 'demandId' : 'orderId'
    if (
      !validAdminBaseURL(`${url.origin}${url.pathname}`) ||
      url.username ||
      url.password ||
      url.hash ||
      !/\/demand-management\/?$/.test(url.pathname) ||
      url.searchParams.get('tab') !== tab ||
      url.searchParams.get(idKey) !== delivery.entity_id ||
      [...url.searchParams].length !== 2 ||
      [...url.searchParams.keys()].some((k) => k !== 'tab' && k !== idKey)
    ) {
      return undefined
    }
    return url.toString()
  } catch {
    return undefined
  }
}
