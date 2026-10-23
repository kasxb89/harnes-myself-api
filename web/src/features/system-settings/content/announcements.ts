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
import * as z from 'zod'

import { getPreviewText } from '@/features/dashboard/lib/text'

export type Announcement = {
  id: number
  title?: string
  content: string
  publishDate: string
  updatedAt?: string | null
  type: 'default' | 'ongoing' | 'success' | 'warning' | 'error'
  extra?: string
}

export const MAX_ANNOUNCEMENTS = 100

export const announcementSchema = z.object({
  title: z
    .string()
    .max(120, 'Title must be less than 120 characters')
    .optional(),
  content: z
    .string()
    .min(1, 'Content is required')
    .max(500, 'Content must be less than 500 characters')
    .refine((value) => value.trim().length > 0, 'Content is required'),
  publishDate: z
    .string()
    .min(1, 'Publish date is required')
    .refine(
      (value) => z.iso.datetime({ offset: true }).safeParse(value).success,
      'Enter a valid publish date'
    ),
  type: z.enum(['default', 'ongoing', 'success', 'warning', 'error']),
  extra: z
    .string()
    .max(100, 'Extra must be less than 100 characters')
    .optional(),
})

export type AnnouncementFormValues = z.infer<typeof announcementSchema>

// Keep legacy records readable while rejecting malformed arrays instead of
// replacing their contents with an empty draft that could be saved by mistake.
export function parseAnnouncements(data: string): Announcement[] {
  if (new TextEncoder().encode(data).byteLength > 1024 * 1024) {
    throw new Error('Invalid announcements')
  }
  const parsed: unknown = JSON.parse(data || '[]')
  if (!Array.isArray(parsed)) throw new Error('Invalid announcements')
  const reservedIds = new Set<number>()
  for (const item of parsed) {
    if (
      item &&
      typeof item.id === 'number' &&
      Number.isSafeInteger(item.id) &&
      item.id > 0
    ) {
      reservedIds.add(item.id)
    }
  }
  const ids = new Set<number>()
  let nextId = 1
  return parsed.map((value: unknown) => {
    if (!value || typeof value !== 'object') {
      throw new Error('Invalid announcement')
    }
    const item = value as Record<string, unknown>
    let id = item.id
    if (
      typeof id !== 'number' ||
      !Number.isSafeInteger(id) ||
      id <= 0 ||
      ids.has(id)
    ) {
      while (reservedIds.has(nextId)) nextId++
      id = nextId
      reservedIds.add(nextId)
    }
    if (
      typeof id !== 'number' ||
      !Number.isSafeInteger(id) ||
      id <= 0 ||
      typeof item.content !== 'string' ||
      typeof item.publishDate !== 'string' ||
      (item.title !== undefined && typeof item.title !== 'string') ||
      (item.extra !== undefined && typeof item.extra !== 'string')
    ) {
      throw new Error('Invalid announcement')
    }
    ids.add(id)
    const type = item.type ?? 'default'
    if (!announcementSchema.shape.type.safeParse(type).success) {
      throw new Error('Invalid announcement type')
    }
    return {
      id,
      title: item.title as string | undefined,
      content: item.content,
      publishDate: item.publishDate,
      updatedAt:
        parsed.length <= MAX_ANNOUNCEMENTS && typeof item.updatedAt === 'string'
          ? item.updatedAt
          : null,
      type: type as Announcement['type'],
      extra: item.extra as string | undefined,
    }
  })
}

export function announcementTitle(
  announcement: Pick<Announcement, 'title' | 'content'>
): string {
  return announcement.title?.trim() || getPreviewText(announcement.content)
}

// The update timestamp is server metadata; never send a locally supplied value.
export function serializeAnnouncements(announcements: Announcement[]): string {
  return JSON.stringify(
    announcements.map((announcement) => ({
      id: announcement.id,
      title: announcement.title,
      content: announcement.content,
      publishDate: announcement.publishDate,
      type: announcement.type,
      extra: announcement.extra,
    }))
  )
}
