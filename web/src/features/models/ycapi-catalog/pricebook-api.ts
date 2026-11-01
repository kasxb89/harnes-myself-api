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
import { useQuery } from '@tanstack/react-query'
import { z } from 'zod'

import type { PricingModel } from '@/features/pricing/types'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import type { ModelPricebookState } from './pricebook-state'

const quoteSchema = z.object({
  model_name: z.string(),
  name: z.string(),
  provider: z.string(),
  basis: z.enum(['approved_snapshot', 'public_reference_estimate']),
  checked_at: z.string(),
  source: z.string(),
  prices: z.array(
    z.object({
      params: z.record(z.string(), z.unknown()),
      direction: z.string(),
      unit: z.string(),
      procurement_cny: z.number().nonnegative(),
      retail_cny: z.number().nonnegative(),
      lane_id: z.string().optional(),
    })
  ),
})
const costSchema = z.object({
  channel_id: z.number().int().positive(),
  model_name: z.string(),
  effective_from: z.number().nonnegative(),
  effective_until: z.number().optional(),
  kind: z.enum([
    'token',
    'request',
    'second',
    'image',
    'image_resolution',
    'h3_video',
  ]),
  source: z.string(),
  failure_refunded: z.boolean(),
  unit_cost_cny: z.number().nonnegative().optional(),
  input_cost_per_million_cny: z.number().nonnegative().optional(),
  cache_cost_per_million_cny: z.number().nonnegative().optional(),
  output_cost_per_million_cny: z.number().nonnegative().optional(),
  deepseek_peak_multiplier: z.number().optional(),
  resolution_costs_cny: z
    .record(z.string(), z.number().nonnegative())
    .optional(),
  extra_image_cost_cny: z.number().nonnegative().optional(),
  free_input_images: z.number().int().nonnegative().optional(),
})
const pricingShape = z.object({
  model_name: z.string(),
  quota_type: z.number(),
  model_ratio: z.number(),
  completion_ratio: z.number(),
  enable_groups: z.array(z.string()),
})
const pricebookSchema = z.object({
  schema_version: z.literal(1),
  generated_at: z.number(),
  usd_exchange_rate: z.number().positive(),
  group_ratios: z.record(z.string(), z.number().nonnegative()).default({}),
  rows: z.array(
    z.object({
      model_name: z.string(),
      channel_id: z.number().int().nonnegative(),
      state: z.enum(['enabled', 'disabled', 'not_configured', 'excluded']),
      origin: z.string().default('channel'),
      configured_groups: z.array(z.string()).default([]),
      expressions: z.record(z.string(), z.string()).default({}),
      approved_quote: quoteSchema.nullable().default(null),
      channel_name: z.string(),
      channel_type: z.number(),
      upstream_host: z.string(),
      channel_status: z.number(),
      enabled_groups: z.array(z.string()),
      // Pricing is the existing server Pricing DTO consumed by ModelPriceCell.
      pricing: z
        .custom<PricingModel>((value) => pricingShape.safeParse(value).success)
        .nullable(),
      cost_rule: costSchema.nullable(),
    })
  ),
})
export type ConfiguredRow = z.infer<typeof pricebookSchema>['rows'][number]

export type ConfirmedQuote = z.infer<typeof quoteSchema>
export function useConfiguredPricebook() {
  const user = useAuthStore((state) => state.auth.user)
  const sid = useAuthStore((state) => state.auth.session?.sid)
  return useQuery({
    queryKey: ['configured-model-pricebook', user?.id, user?.role, sid],
    enabled: Boolean(user && user.role >= ROLE.SUPER_ADMIN),
    queryFn: async ({ signal }) =>
      pricebookSchema.parse(
        requireServerSuccess(
          (
            await api.get('/api/model-pricebook', {
              signal,
              disableDuplicate: true,
            })
          ).data
        ).data
      ),
    staleTime: 10_000,
    retry: false,
    meta: { errorToast: false },
  })
}
export function pricebookStatesByModel(
  rows: ConfiguredRow[]
): Record<string, ModelPricebookState> {
  const states: Record<string, ModelPricebookState> = {}
  for (const row of rows) {
    if (!states[row.model_name] || row.state === 'enabled') {
      states[row.model_name] = row.state
    }
  }
  return states
}
