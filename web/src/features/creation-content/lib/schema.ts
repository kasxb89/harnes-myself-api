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

const id = z.string().regex(/^[a-zA-Z0-9][a-zA-Z0-9_-]{0,79}$/)
const assetId = z
  .string()
  .regex(/^cc_[a-f0-9]{64}$/)
  .nullable()
const order = z.number().int().min(-100000).max(100000)
const status = z.enum(['draft', 'published'])
const localized = (limit: number, required = true) =>
  z.object({
    zh: z
      .string()
      .max(limit)
      .refine((value) => !required || value.trim().length > 0),
    en: z.string().max(limit).optional(),
  })
export const categorySchema = z.object({
  id,
  label: localized(100),
  sortOrder: order,
  enabled: z.boolean(),
})
export const templateSchema = z.object({
  id,
  categoryId: id.nullable(),
  title: localized(200),
  description: localized(2000),
  prompt: localized(7000),
  coverAssetId: assetId,
  demoVideoAssetId: assetId,
  sortOrder: order,
  status,
})
export const presetSchema = z.object({
  id,
  label: localized(100),
  promptSuffix: localized(2000),
  sortOrder: order,
  status,
})
const modeSchema = z.object({
  promptPlaceholder: localized(1000, false),
  title: localized(200, false),
  description: localized(2000, false),
  categories: z.array(categorySchema).max(100),
  templates: z.array(templateSchema).max(500),
  presets: z.array(presetSchema).max(100),
})
export const contentSchema = z
  .object({
    schemaVersion: z.literal(1),
    revision: z.string().max(100),
    image: modeSchema,
    video: modeSchema,
  })
  .superRefine((content, context) => {
    const used = new Set<string>()
    for (const modeName of ['image', 'video'] as const) {
      const mode = content[modeName]
      const categories = new Set(mode.categories.map((item) => item.id))
      for (const key of ['categories', 'templates', 'presets'] as const) {
        mode[key].forEach((item, index) => {
          if (used.has(item.id)) {
            context.addIssue({
              code: 'custom',
              path: [modeName, key, index, 'id'],
              message: 'IDs must be unique',
            })
          }
          used.add(item.id)
        })
      }
      mode.templates.forEach((item, index) => {
        if (item.categoryId !== null && !categories.has(item.categoryId)) {
          context.addIssue({
            code: 'custom',
            path: [modeName, 'templates', index, 'categoryId'],
            message: 'Choose an existing category',
          })
        }
        if (item.status === 'published' && item.coverAssetId === null) {
          context.addIssue({
            code: 'custom',
            path: [modeName, 'templates', index, 'coverAssetId'],
            message: 'Published templates require a cover',
          })
        }
      })
    }
  })
export type CreationContent = z.infer<typeof contentSchema>
export type ModeContent = z.infer<typeof modeSchema>
export type Category = z.infer<typeof categorySchema>
export type Template = z.infer<typeof templateSchema>
export type Preset = z.infer<typeof presetSchema>
export type LocalizedText = { zh: string; en?: string }
export type Mode = 'image' | 'video'
export type CreationAsset = {
  id: string
  mediaType: string
  size: number
  url: string
}
