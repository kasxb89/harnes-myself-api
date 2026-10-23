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

import type {
  ClientModelPresentation,
  ClientModelPresentationEntry,
} from '../types'

const presentationEntrySchema = z
  .object({
    description: z.string().max(500).optional(),
    badge: z
      .string()
      .max(32)
      .refine((value) => !/[\r\n\u0085\u2028\u2029]/.test(value))
      .optional(),
  })
  .strict()

// Validate own entries without Zod's record reconstruction: exact IDs such as
// "__proto__" are valid and must survive a later whole-map save unchanged.
export const clientModelPresentationSchema = z.custom<ClientModelPresentation>(
  (value) => {
    if (typeof value !== 'object' || value === null || Array.isArray(value)) {
      return false
    }
    const dto = value as Record<string, unknown>
    if (
      dto.schema_version !== 1 ||
      Object.keys(dto).some(
        (key) => key !== 'schema_version' && key !== 'models'
      )
    ) {
      return false
    }
    if (
      typeof dto.models !== 'object' ||
      dto.models === null ||
      Array.isArray(dto.models)
    ) {
      return false
    }
    const entries = Object.entries(dto.models)
    return (
      entries.length <= 256 &&
      entries.every(
        ([id, entry]) =>
          id.length > 0 &&
          !/^\p{White_Space}|\p{White_Space}$/u.test(id) &&
          new TextEncoder().encode(id).length <= 255 &&
          !/\p{Cc}/u.test(id) &&
          presentationEntrySchema.safeParse(entry).success
      )
    )
  },
  { message: 'Invalid client model presentation' }
)

export function sameClientModelPresentation(
  left: ClientModelPresentation,
  right: ClientModelPresentation
): boolean {
  const ids = Object.keys(left.models)
  return (
    ids.length === Object.keys(right.models).length &&
    ids.every(
      (id) =>
        Object.hasOwn(right.models, id) &&
        left.models[id].description === right.models[id].description &&
        left.models[id].badge === right.models[id].badge
    )
  )
}

const presentationFieldSchema = z.object({
  mode: z.enum(['default', 'custom', 'hidden']),
  text: z.string(),
})

export const presentationEditorSchema = z
  .object({
    description: presentationFieldSchema,
    badge: presentationFieldSchema,
  })
  .superRefine((value, context) => {
    for (const field of ['description', 'badge'] as const) {
      if (value[field].mode !== 'custom') continue
      const text = value[field].text
      if (text.length === 0) {
        context.addIssue({
          code: 'custom',
          path: [field, 'text'],
          message: 'Enter custom text or choose another display mode.',
        })
      }
      if (text.length > (field === 'description' ? 500 : 32)) {
        context.addIssue({
          code: 'custom',
          path: [field, 'text'],
          message:
            field === 'description'
              ? 'Description must be 500 characters or fewer.'
              : 'Badge must be 32 characters or fewer.',
        })
      }
      if (field === 'badge' && /[\r\n\u0085\u2028\u2029]/.test(text)) {
        context.addIssue({
          code: 'custom',
          path: [field, 'text'],
          message: 'Badge must be a single line.',
        })
      }
    }
  })

export type PresentationEditorValues = z.infer<typeof presentationEditorSchema>

export function presentationEntryToForm(
  entry: ClientModelPresentationEntry
): PresentationEditorValues {
  const values = {} as PresentationEditorValues
  for (const field of ['description', 'badge'] as const) {
    const text = entry[field]
    let mode: PresentationEditorValues['description']['mode'] = 'custom'
    if (text === undefined) mode = 'default'
    else if (text === '') mode = 'hidden'
    values[field] = { mode, text: text ?? '' }
  }
  return values
}

export function presentationFormToEntry(
  values: PresentationEditorValues
): ClientModelPresentationEntry {
  const entry: ClientModelPresentationEntry = {}
  for (const field of ['description', 'badge'] as const) {
    if (values[field].mode === 'hidden') entry[field] = ''
    else if (values[field].mode === 'custom') entry[field] = values[field].text
  }
  return entry
}
