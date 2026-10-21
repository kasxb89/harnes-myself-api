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

import type { ClientModelConfig, ClientModelOption } from '../types'

export const MAX_CLIENT_MODEL_ORDER = 256

const modelIdSchema = z
  .string()
  .refine((value) => new TextEncoder().encode(value).length <= 255, {
    message: 'Invalid client model configuration',
  })

export const clientModelConfigSchema = z
  .object({
    schema_version: z.literal(1),
    default_chat_model: modelIdSchema,
    model_order: z
      .array(modelIdSchema.refine((value) => value.length > 0))
      .max(MAX_CLIENT_MODEL_ORDER),
  })
  .strict()
  .refine(
    (value) => new Set(value.model_order).size === value.model_order.length,
    {
      path: ['model_order'],
      message: 'Invalid client model configuration',
    }
  )

export const clientModelRankSchema = z
  .string()
  .refine(
    (value) =>
      value === '' ||
      (/^[0-9]{1,6}$/.test(value) &&
        Number(value) >= 1 &&
        Number(value) <= 999999),
    { message: 'Enter a whole number from 1 to 999999, or leave blank.' }
  )

export const clientModelConfigFormSchema = z.object({
  default_chat_model: modelIdSchema,
  model_ranks: z
    .array(z.object({ id: modelIdSchema, rank: clientModelRankSchema }))
    .refine(
      (ranks) =>
        ranks.filter((entry) => entry.rank !== '').length <=
        MAX_CLIENT_MODEL_ORDER,
      { message: 'You can prioritize up to 256 models.' }
    ),
})

export type ClientModelConfigFormValues = z.infer<
  typeof clientModelConfigFormSchema
>

export function clientModelConfigToForm(
  config: ClientModelConfig
): ClientModelConfigFormValues {
  return {
    default_chat_model: config.default_chat_model,
    model_ranks: config.model_order.map((id, index) => ({
      id,
      rank: String(index + 1),
    })),
  }
}

export function buildClientModelOrder(
  ranks: ClientModelConfigFormValues['model_ranks'],
  models: ClientModelOption[]
): string[] {
  const byId = new Map(ranks.map((entry) => [entry.id, entry.rank]))
  const catalogIds = models.map((model) => model.id)
  const catalogSet = new Set(catalogIds)
  return [
    ...catalogIds,
    ...ranks.map((entry) => entry.id).filter((id) => !catalogSet.has(id)),
  ]
    .filter((id) => byId.has(id) && byId.get(id) !== '')
    .sort((left, right) => Number(byId.get(left)) - Number(byId.get(right)))
}
