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

const count = z.number().int().nonnegative().max(Number.MAX_SAFE_INTEGER)
const poolId = z.string().min(1).max(256)
export const mediaConcurrencySchema = z.object({
  schema_version: z.literal(1),
  revision: z.string().min(1).max(256),
  defaults: z.object({
    video: z.number().int().min(1).max(64),
    image: z.number().int().min(1).max(64),
    audio: z.number().int().min(1).max(64).optional(),
  }),
  provider_defaults: z
    .object({
      ycapi: z.object({
        video: z.number().int().min(1).max(64),
        image: z.number().int().min(1).max(64),
        audio: z.number().int().min(1).max(64),
      }),
    })
    .optional(),
  pool_overrides: z
    .array(
      z.object({ pool_id: poolId, limit: z.number().int().min(1).max(64) })
    )
    .max(128),
  pools: z
    .array(
      z.object({
        pool_id: poolId,
        label: z.string().min(1).max(512),
        media_kind: z.enum(['video', 'image', 'audio']),
        channel_ids: z.array(z.number().int().positive()).max(10000),
        configured_limit: z.number().int().positive().max(64),
        known_upstream_limit: z.number().int().positive().max(64).nullable(),
        effective_limit: z.number().int().positive().max(64),
        occupied: count,
        unknown: count,
        enabled: z.boolean(),
        reconciled: z.boolean(),
        available_slots: count,
      })
    )
    .max(5000),
})
export type MediaConcurrency = z.infer<typeof mediaConcurrencySchema>
export type MediaPool = MediaConcurrency['pools'][number]
export type MediaConcurrencyUpdate = Pick<
  MediaConcurrency,
  'schema_version' | 'defaults' | 'provider_defaults' | 'pool_overrides'
> & { expected_revision: string }

const whole = (max: number) =>
  z
    .string()
    .refine(
      (value) => /^[1-9]\d*$/.test(value) && Number(value) <= max,
      'Enter a whole number within the allowed range'
    )
export const mediaConcurrencyFormSchema = z
  .object({
    video: whole(64),
    image: whole(64),
    audio: whole(64),
    ycapi_video: whole(64),
    ycapi_image: whole(64),
    ycapi_audio: whole(64),
    pools: z
      .array(
        z.object({
          pool_id: poolId,
          media_kind: z.enum(['video', 'image', 'audio']),
          limit: z.string(),
        })
      )
      .max(5000),
  })
  .superRefine((values, context) => {
    let explicit = 0
    values.pools.forEach((pool, index) => {
      if (pool.limit === '') return
      explicit++
      if (!/^[1-9]\d*$/.test(pool.limit) || Number(pool.limit) > 64) {
        context.addIssue({
          code: 'custom',
          path: ['pools', index, 'limit'],
          message: 'Enter a whole number within the allowed range',
        })
      }
    })
    if (explicit > 128) {
      context.addIssue({
        code: 'custom',
        path: ['pools'],
        message: 'At most 128 pool overrides are allowed',
      })
    }
  })
export type MediaConcurrencyFormValues = z.infer<
  typeof mediaConcurrencyFormSchema
>

export function concurrencyToForm(
  snapshot: MediaConcurrency
): MediaConcurrencyFormValues {
  return {
    video: String(snapshot.defaults.video),
    image: String(snapshot.defaults.image),
    audio: String(snapshot.defaults.audio ?? 1),
    ycapi_video: String(snapshot.provider_defaults?.ycapi.video ?? 20),
    ycapi_image: String(snapshot.provider_defaults?.ycapi.image ?? 20),
    ycapi_audio: String(snapshot.provider_defaults?.ycapi.audio ?? 20),
    pools: snapshot.pools.map((pool) => ({
      pool_id: pool.pool_id,
      media_kind: pool.media_kind,
      limit: String(
        snapshot.pool_overrides.find((entry) => entry.pool_id === pool.pool_id)
          ?.limit ?? ''
      ),
    })),
  }
}

export function concurrencyUpdate(
  snapshot: MediaConcurrency,
  values: MediaConcurrencyFormValues
): MediaConcurrencyUpdate {
  const visible = new Set(values.pools.map((pool) => pool.pool_id))
  return {
    schema_version: 1,
    expected_revision: snapshot.revision,
    defaults: {
      video: Number(values.video),
      image: Number(values.image),
      audio: Number(values.audio),
    },
    provider_defaults: {
      ycapi: {
        video: Number(values.ycapi_video),
        image: Number(values.ycapi_image),
        audio: Number(values.ycapi_audio),
      },
    },
    pool_overrides: [
      ...snapshot.pool_overrides.filter((pool) => !visible.has(pool.pool_id)),
      ...values.pools
        .filter((pool) => pool.limit !== '')
        .map((pool) => ({ pool_id: pool.pool_id, limit: Number(pool.limit) })),
    ],
  }
}
