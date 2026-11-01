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

export const ANNOUNCEMENTS_QUERY_KEY = ['announcements-settings'] as const
export const announcementsSettingsSchema = z
  .object({
    'console_setting.announcements': z.string(),
    'console_setting.announcements_enabled': z.enum(['true', 'false']),
  })
  .strict()
export type AnnouncementsSettings = z.infer<typeof announcementsSettingsSchema>
type Response = {
  success: boolean
  message?: string
  data: AnnouncementsSettings
}
export async function loadAnnouncements(
  signal?: AbortSignal
): Promise<AnnouncementsSettings> {
  const response = await api.get<Response>('/api/announcements', {
    signal,
    disableDuplicate: true,
  })
  return announcementsSettingsSchema.parse(
    requireServerSuccess(response.data).data
  )
}
export async function saveAnnouncements(
  value: AnnouncementsSettings
): Promise<AnnouncementsSettings> {
  const response = await api.put<Response>(
    '/api/announcements',
    announcementsSettingsSchema.parse(value)
  )
  return announcementsSettingsSchema.parse(
    requireServerSuccess(response.data).data
  )
}

export type AnnouncementTopBanner = { schema_version: 1; enabled: boolean }
export async function loadAnnouncementTopBanner(): Promise<AnnouncementTopBanner> {
  const r = await api.get<{ success: boolean; data: AnnouncementTopBanner }>(
    '/api/announcements/top-banner'
  )
  const value = requireServerSuccess(r.data).data
  if (value?.schema_version !== 1 || typeof value.enabled !== 'boolean') {
    throw new Error('Invalid client model configuration')
  }
  return value
}
export async function saveAnnouncementTopBanner(
  value: AnnouncementTopBanner
): Promise<AnnouncementTopBanner> {
  const r = await api.put<{ success: boolean; data: AnnouncementTopBanner }>(
    '/api/announcements/top-banner',
    value
  )
  return requireServerSuccess(r.data).data
}
