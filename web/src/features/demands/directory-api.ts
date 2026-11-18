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

export const profileSchema = z.object({
  id: z.string(),
  code: z.string().regex(/^[A-Za-z0-9_-]{1,80}$/),
  display_name: z.string().min(1).max(100),
  service_direction: z.string().max(200).default(''),
  introduction: z.string().max(2000),
  category_ids: z.array(z.string()).max(30).default([]),
  avatar_attachment_id: z.string(),
  published: z.boolean().default(false),
  enabled: z.boolean(),
  sort: z.number().int().min(-100000).max(100000).default(0),
  organization: z.string().max(200).default(''),
  notes: z.string().max(4000).default(''),
  user_id: z.number().int().min(0).default(0),
  revision: z.number().int().min(0),
})
export type DirectoryProfile = z.output<typeof profileSchema>
export type DirectoryKind = 'service-profiles' | 'customer-profiles'
export const newDirectoryProfile: DirectoryProfile = {
  id: '',
  code: '',
  display_name: '',
  service_direction: '',
  introduction: '',
  category_ids: [],
  avatar_attachment_id: '',
  published: false,
  enabled: true,
  sort: 0,
  organization: '',
  notes: '',
  user_id: 0,
  revision: 0,
}
type Envelope = { success: boolean; data: unknown; message?: string }
export async function loadDirectory(
  kind: DirectoryKind,
  q: string,
  after: string,
  signal?: AbortSignal
) {
  const r = await api.get<Envelope>(`/api/demand-management/${kind}`, {
    params: { q, after_id: after, limit: 30 },
    signal,
    disableDuplicate: true,
  })
  return z
    .object({
      items: z.array(profileSchema),
      next_after_id: z.string(),
      has_more: z.boolean(),
      total: z.number(),
    })
    .parse(requireServerSuccess(r.data).data)
}
export async function saveDirectoryProfile(
  kind: DirectoryKind,
  profile: DirectoryProfile
) {
  const base = `/api/demand-management/${kind}`
  const r = profile.id
    ? await api.put<Envelope>(
        `${base}/${encodeURIComponent(profile.id)}`,
        profile
      )
    : await api.post<Envelope>(base, profile)
  return profileSchema.parse(requireServerSuccess(r.data).data)
}
export async function importDirectoryPreset() {
  const r = await api.post<Envelope>(
    '/api/demand-management/profile-presets',
    { batch_code: 'yunshu-platform-services-customers-100x100-v1' },
    { timeout: 120000 }
  )
  return z
    .object({
      created_service_profiles: z.number(),
      created_customer_profiles: z.number(),
      skipped_service_profiles: z.number(),
      skipped_customer_profiles: z.number(),
      replayed: z.boolean(),
    })
    .parse(requireServerSuccess(r.data).data)
}
