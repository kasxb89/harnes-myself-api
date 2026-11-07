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

import type { ClientModelCapacityRoute } from '../types'

export const clientModelCapacityCatalogSchema = z.object({
  revision: z.string().min(1),
  routes: z.array(
    z.object({
      model: z.string().min(1),
      group: z.string().min(1),
      channel_id: z.number().int().positive(),
      channel_name: z.string(),
      route_model: z.string().min(1),
      context_window: z.number().int().positive().optional(),
      max_output_tokens: z.number().int().positive().optional(),
      budget_field: z.string().min(1),
      source: z.string(),
      verified_at: z.string(),
      configured: z.boolean(),
      valid: z.boolean(),
      requires_full_capacity: z.boolean().optional(),
    })
  ),
})

const tokenCount = z
  .string()
  .regex(/^[1-9]\d*$/, 'Enter a positive whole token count')
  .or(z.literal(''))
export const capacityFormSchema = z
  .object({
    context_window: tokenCount.refine(
      (value) => Number(value) <= 2147483647,
      'Context window is too large'
    ),
    max_output_tokens: tokenCount.refine(
      (value) => Number(value) <= 1073741823,
      'Maximum output is too large'
    ),
    source: z
      .string()
      .trim()
      .min(1, 'Provide the upstream specification source')
      .max(2048),
    verified_at: z
      .string()
      .regex(/^\d{4}-\d{2}-\d{2}$/, 'Enter a verification date'),
  })
  .refine(
    (value) => value.context_window !== '' || value.max_output_tokens !== '',
    {
      message: 'Enter at least one token limit',
      path: ['context_window'],
    }
  )
  .refine(
    (value) =>
      !value.context_window ||
      !value.max_output_tokens ||
      Number(value.max_output_tokens) <= Number(value.context_window),
    {
      message: 'Maximum output must not exceed the context window',
      path: ['max_output_tokens'],
    }
  )

export type CapacityFormValues = z.infer<typeof capacityFormSchema>

export function capacitySchemaForRoutes(routes: ClientModelCapacityRoute[]) {
  return capacityFormSchema.refine(
    (value) =>
      !routes.some((row) => row.requires_full_capacity) ||
      (value.context_window !== '' && value.max_output_tokens !== ''),
    {
      message: 'Both token limits are required for this model',
      path: ['context_window'],
    }
  )
}

/** Mixed values stay blank so applying to all groups is an explicit edit. */
export function capacityRoutesToForm(
  routes: ClientModelCapacityRoute[]
): CapacityFormValues {
  const first = routes[0]
  const context = first?.context_window
  const output = first?.max_output_tokens
  const source = first?.source ?? ''
  const date = first?.verified_at ?? ''
  return {
    context_window:
      context !== undefined &&
      routes.every((row) => row.context_window === context)
        ? String(context)
        : '',
    max_output_tokens:
      output !== undefined &&
      routes.every((row) => row.max_output_tokens === output)
        ? String(output)
        : '',
    source: routes.every((row) => row.source === source) ? source : '',
    verified_at: routes.every((row) => row.verified_at === date) ? date : '',
  }
}
