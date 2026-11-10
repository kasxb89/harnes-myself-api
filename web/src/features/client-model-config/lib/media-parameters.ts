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

// The finite editable vocabulary in model/model_configuration_parameters.go.
export const MEDIA_PARAMETERS = {
  resolution: ['string', 'Resolution'],
  size: ['string', 'Image size'],
  ratio: ['string', 'Aspect ratio'],
  aspect_ratio: ['string', 'Aspect ratio'],
  seconds: ['integer', 'Video duration (seconds)'],
  duration: ['integer', 'Video duration (seconds)'],
  quality: ['string', 'Quality'],
  mode: ['string', 'Generation mode'],
  n: ['integer', 'Image count'],
  count: ['integer', 'Image count'],
  image_count: ['integer', 'Image count'],
  reference_images: ['array', 'Reference images'],
  reference_videos: ['array', 'Reference videos'],
  reference_audios: ['array', 'Reference audio'],
  frame_images: ['array', 'Frame images'],
  input_images: ['array', 'Input images'],
  images: ['array', 'Input images'],
  generate_audio: ['boolean', 'Generate audio'],
  audio: ['boolean', 'Audio'],
  audio_output: ['boolean', 'Audio output'],
  watermark: ['boolean', 'Watermark'],
  seed: ['integer', 'Seed'],
  background: ['string', 'Background'],
  format: ['string', 'Output format'],
  output_format: ['string', 'Output format'],
  moderation: ['string', 'Moderation'],
  response_format: ['string', 'Response format'],
  mix_frames_and_references: ['boolean', 'Mix frames and references'],
} as const
export type MediaParameterName = keyof typeof MEDIA_PARAMETERS
export const isMediaParameterName = (
  name: string
): name is MediaParameterName => Object.hasOwn(MEDIA_PARAMETERS, name)
const fieldSchema = z
  .object({
    type: z.string().optional(),
    enum: z.array(z.unknown()).optional(),
    const: z.unknown().optional(),
    default: z.unknown().optional(),
    minimum: z.number().optional(),
    maximum: z.number().optional(),
    minItems: z.number().optional(),
    maxItems: z.number().optional(),
    items: z.record(z.string(), z.unknown()).optional(),
  })
  .passthrough()
const objectSchema = z
  .object({
    type: z.string().optional(),
    properties: z
      .record(z.string(), z.union([z.literal(false), fieldSchema]))
      .optional(),
    required: z.array(z.string()).optional(),
  })
  .passthrough()
const documentSchema = objectSchema.extend({
  oneOf: z.array(objectSchema).optional(),
})
export type MediaParameterField = z.infer<typeof fieldSchema>
export type MediaParameterObject = z.infer<typeof objectSchema>
export type MediaParameterDocument = z.infer<typeof documentSchema>
export const MEDIA_CONSTRAINT_KEYS = [
  'type',
  'enum',
  'const',
  'default',
  'minimum',
  'maximum',
  'minItems',
  'maxItems',
  'items',
]

export function readMediaParameters(
  value: string | Record<string, unknown> | undefined
): MediaParameterDocument | null {
  try {
    let raw: unknown = value ?? {}
    if (typeof value === 'string') raw = value.trim() ? JSON.parse(value) : {}
    const result = documentSchema.safeParse(raw)
    return result.success ? result.data : null
  } catch {
    return null
  }
}

export function parameterSeed(
  name: MediaParameterName,
  value?: MediaParameterField | false
): MediaParameterField {
  const field: MediaParameterField = { type: MEDIA_PARAMETERS[name][0] }
  if (value) {
    for (const key of MEDIA_CONSTRAINT_KEYS) {
      if (Object.hasOwn(value, key)) field[key] = value[key]
    }
  }
  field.type = MEDIA_PARAMETERS[name][0]
  if (field.type === 'array') field.items = { type: 'string' }
  return field
}

/** Native metadata remains read-only. Only named generation controls are copied into a draft. */
export function nativeMediaFields(
  native: MediaParameterDocument
): Record<string, MediaParameterField | false> {
  const result = { ...native.properties }
  for (const branch of native.oneOf ?? []) {
    for (const [name, value] of Object.entries(branch.properties ?? {})) {
      if (
        !isMediaParameterName(name) ||
        result[name] === false ||
        value === false
      ) {
        continue
      }
      const current = {
        ...(result[name] || { type: MEDIA_PARAMETERS[name][0] }),
      }
      const explicit = native.properties?.[name]
      if (
        (!explicit || (!explicit.enum && !Object.hasOwn(explicit, 'const'))) &&
        (value.enum || Object.hasOwn(value, 'const'))
      ) {
        const menu = value.enum ?? [value.const]
        const existing =
          current.enum ??
          (Object.hasOwn(current, 'const') ? [current.const] : [])
        current.enum = [
          ...new Map(
            [...existing, ...menu].map((item) => [JSON.stringify(item), item])
          ).values(),
        ]
      }
      for (const key of ['minimum', 'minItems'] as const) {
        if (
          typeof value[key] === 'number' &&
          (!explicit || explicit[key] === undefined)
        ) {
          current[key] = Math.min(current[key] ?? value[key], value[key])
        }
      }
      for (const key of ['maximum', 'maxItems'] as const) {
        if (
          typeof value[key] === 'number' &&
          (!explicit || explicit[key] === undefined)
        ) {
          current[key] = Math.max(current[key] ?? value[key], value[key])
        }
      }
      result[name] = current
    }
  }
  if (native.oneOf?.length) {
    for (const [name, field] of Object.entries(result)) {
      if (!field) continue
      const original = native.properties?.[name]
      const candidates = native.oneOf.map((branch) => branch.properties?.[name])
      const copy = { ...field }
      for (const key of [
        'minimum',
        'maximum',
        'minItems',
        'maxItems',
      ] as const) {
        if (
          (!original || original[key] === undefined) &&
          candidates.some(
            (candidate) => !candidate || candidate[key] === undefined
          )
        ) {
          delete copy[key]
        }
      }
      if (
        (!original || (!original.enum && !Object.hasOwn(original, 'const'))) &&
        candidates.some(
          (candidate) =>
            !candidate ||
            (!candidate.enum && !Object.hasOwn(candidate, 'const'))
        )
      ) {
        delete copy.enum
      }
      result[name] = copy
    }
  }
  const limits = z
    .record(z.string(), z.unknown())
    .safeParse(native['x-reference-limits'])
  if (limits.success) {
    for (const [name, key] of [
      ['reference_images', 'images'],
      ['reference_videos', 'videos'],
      ['reference_audios', 'audios'],
    ] as const) {
      const maximum = limits.data[key]
      if (!Object.hasOwn(result, name) && typeof maximum === 'number') {
        result[name] = {
          type: 'array',
          items: { type: 'string' },
          maxItems: maximum,
        }
      }
    }
  }
  const frames = native['x-reference-frame-max-images']
  if (!Object.hasOwn(result, 'frame_images') && typeof frames === 'number') {
    result.frame_images = {
      type: 'array',
      items: { type: 'string' },
      maxItems: frames,
    }
  }
  return result
}

export function serializeMediaParameters(
  document: MediaParameterDocument
): string {
  if (
    !Object.keys(document.properties ?? {}).length &&
    !document.oneOf &&
    !document.required?.length &&
    Object.keys(document).every((key) =>
      ['type', 'properties', 'required'].includes(key)
    )
  ) {
    return ''
  }
  return JSON.stringify(document, null, 2)
}

export function updateMediaField(
  document: MediaParameterDocument,
  name: MediaParameterName,
  field: MediaParameterField | false | undefined
): MediaParameterDocument {
  const properties = { ...document.properties }
  if (field === undefined) delete properties[name]
  else properties[name] = field
  const result = { ...document, type: 'object', properties }
  if (field === false || field === undefined) {
    if (result.required) {
      result.required = result.required.filter((key) => key !== name)
    }
  }
  return result
}

export function updateMediaRequired(
  document: MediaParameterDocument,
  native: MediaParameterDocument,
  name: MediaParameterName,
  required: boolean
): MediaParameterDocument | null {
  const names = new Set(document.required ?? native.required ?? [])
  if (required) names.add(name)
  else names.delete(name)
  if ([...names].some((key) => !isMediaParameterName(key))) return null
  const fields = nativeMediaFields(native)
  const properties = { ...document.properties }
  for (const key of names) {
    if (!Object.hasOwn(properties, key) && isMediaParameterName(key)) {
      properties[key] = parameterSeed(key, fields[key])
    }
  }
  return { ...document, type: 'object', properties, required: [...names] }
}

/** Import only constraints supported by the existing API; never discard an unknown native restriction. */
export function importMediaCombinations(
  document: MediaParameterDocument,
  native: MediaParameterDocument
): MediaParameterDocument | null {
  const properties = { ...document.properties }
  const branches: MediaParameterObject[] = []
  for (const branch of native.oneOf ?? []) {
    if (
      Object.keys(branch).some(
        (key) =>
          !['type', 'properties', 'required', 'title', 'description'].includes(
            key
          )
      )
    ) {
      return null
    }
    const fields: NonNullable<MediaParameterObject['properties']> = {}
    for (const [name, field] of Object.entries(branch.properties ?? {})) {
      if (
        !isMediaParameterName(name) ||
        (field &&
          Object.keys(field).some(
            (key) => !MEDIA_CONSTRAINT_KEYS.includes(key)
          ))
      ) {
        return null
      }
      fields[name] = field === false ? false : parameterSeed(name, field)
      if (!Object.hasOwn(properties, name)) {
        properties[name] = parameterSeed(name)
      }
    }
    branches.push({ ...branch, type: 'object', properties: fields })
  }
  // Native annotations are not generation constraints and must not enter the editable API vocabulary.
  const oneOf = branches.map(({ properties, required }) =>
    required
      ? { type: 'object', properties, required }
      : { type: 'object', properties }
  )
  return {
    ...document,
    type: 'object',
    properties,
    ...(oneOf.length ? { oneOf } : {}),
  }
}

export function mediaParameterErrors(
  document: MediaParameterDocument
): string[] {
  const errors: string[] = []
  const check = (
    object: MediaParameterObject,
    parent?: MediaParameterObject
  ) => {
    if (parent && !Object.keys(object.properties ?? {}).length) {
      errors.push('Configure at least one parameter in each combination.')
    }
    for (const [name, field] of Object.entries(object.properties ?? {})) {
      if (!isMediaParameterName(name)) continue // Retain future fields without guessing their controls.
      if (parent && !Object.hasOwn(parent.properties ?? {}, name)) {
        errors.push('A specification combination uses an undeclared parameter.')
      }
      if (field === false) continue
      const type = MEDIA_PARAMETERS[name][0]
      if (field.type !== type) {
        errors.push('A generation parameter has an invalid type.')
      }
      const minimumLimit =
        name === 'seed' || name === 'seconds' || name === 'duration' ? -1 : 0
      let maximumLimit = 2147483647
      if (type === 'array' || ['n', 'count', 'image_count'].includes(name)) {
        maximumLimit = 128
      } else if (name === 'seconds' || name === 'duration') maximumLimit = 3600
      for (const key of [
        'minimum',
        'maximum',
        'minItems',
        'maxItems',
      ] as const) {
        const value = field[key]
        if (
          value !== undefined &&
          (!Number.isSafeInteger(value) ||
            value < minimumLimit ||
            value > maximumLimit)
        ) {
          errors.push('Enter a whole-number limit within the supported range.')
        }
      }
      for (const [min, max] of [
        ['minimum', 'maximum'],
        ['minItems', 'maxItems'],
      ] as const) {
        if (
          field[min] !== undefined &&
          field[max] !== undefined &&
          field[min] > field[max]
        ) {
          errors.push('The minimum cannot exceed the maximum.')
        }
      }
      const matches = (value: unknown, menu: boolean) => {
        if (
          type === 'integer' &&
          (typeof value !== 'number' ||
            !Number.isSafeInteger(value) ||
            (field.minimum !== undefined && value < field.minimum) ||
            (field.maximum !== undefined && value > field.maximum) ||
            (['n', 'count', 'image_count'].includes(name) &&
              (value < 1 || value > 128)) ||
            (['seconds', 'duration'].includes(name) &&
              (value < -1 || value > 3600)))
        ) {
          return false
        }
        if (
          type === 'string' &&
          (typeof value !== 'string' ||
            !value ||
            value.trim() !== value ||
            new TextEncoder().encode(value).length > 128 ||
            /\p{Cc}/u.test(value))
        ) {
          return false
        }
        if (type === 'boolean' && typeof value !== 'boolean') return false
        if (
          type === 'array' &&
          (!Array.isArray(value) ||
            value.some((item) => typeof item !== 'string') ||
            value.length > 128 ||
            (field.minItems !== undefined && value.length < field.minItems) ||
            (field.maxItems !== undefined && value.length > field.maxItems))
        ) {
          return false
        }
        return (
          !menu ||
          ((!Object.hasOwn(field, 'const') ||
            JSON.stringify(value) === JSON.stringify(field.const)) &&
            (!field.enum ||
              field.enum.some(
                (item) => JSON.stringify(item) === JSON.stringify(value)
              )))
        )
      }
      if (
        field.enum &&
        (!field.enum.length ||
          field.enum.length > 256 ||
          new Set(field.enum.map((item) => JSON.stringify(item))).size !==
            field.enum.length ||
          field.enum.some((value) => !matches(value, false)))
      ) {
        errors.push(
          'Choices must be unique valid values within the configured limits.'
        )
      }
      for (const key of ['default', 'const']) {
        if (Object.hasOwn(field, key) && !matches(field[key], true)) {
          errors.push(
            'The default or fixed value must match the allowed choices and limits.'
          )
        }
      }
    }
    if (
      object.required?.some(
        (name) =>
          !Object.hasOwn(object.properties ?? {}, name) ||
          object.properties?.[name] === false
      ) ||
      new Set(object.required ?? []).size !== (object.required ?? []).length
    ) {
      errors.push('Required parameters must be enabled and declared.')
    }
  }
  if (
    Object.keys(document).length &&
    !Object.keys(document.properties ?? {}).length
  ) {
    errors.push('Configure at least one parameter or use adapter settings.')
  }
  check(document)
  if (document.oneOf) {
    if (!document.oneOf.length || document.oneOf.length > 128) {
      errors.push('Add between 1 and 128 valid specification combinations.')
    }
    for (const branch of document.oneOf) check(branch, document)
  }
  return [...new Set(errors)]
}
