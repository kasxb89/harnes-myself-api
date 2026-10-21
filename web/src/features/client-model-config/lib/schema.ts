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
