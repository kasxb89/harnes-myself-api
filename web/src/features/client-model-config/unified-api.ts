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
import { t } from 'i18next'

import type { PricingValues } from '@/features/model-pricing/pricing'
import type { ApiResponse } from '@/features/profile/types'
import { api } from '@/lib/api'
import { createServerError } from '@/lib/server-error-message'

export type UnifiedModelRoute = {
  channel_id: number
  group: string
  upstream_model: string
  enabled: boolean
  text_protocol?: 'openai' | 'ycapi_fixed_lane' | 'legacy' | ''
}
export type OfficialPriceReference = {
  schema_version: 1
  official_cny: string
  currency: 'CNY'
  model: string
  unit: string
  source_url: string
  checked_at: string
  conditions: string
  source_currency: 'CNY' | 'USD'
  usd_cny?: string
  official_total_cny?: string
}
export type OfficialReferenceRow = {
  params: Record<string, string | number | boolean | null>
  direction?: 'input' | 'output'
  unit: string
  official_reference?: OfficialPriceReference
}
export type OfficialReferenceConfiguration = {
  mode: 'inherit' | 'override' | 'disabled'
  rows?: OfficialReferenceRow[]
}
export type UnifiedModelDefinition = {
  model_name: string
  kind: 'text' | 'image' | 'video'
  display_name: string
  enabled: boolean
  routes: UnifiedModelRoute[]
  context_window?: number
  max_output_tokens?: number
  budget_field?: string
  parameters?: Record<string, unknown>
  pricing: PricingValues
  cost_rules?: Record<string, unknown>[]
  official_references?: OfficialReferenceConfiguration
  source: string
}
export type UnifiedModelEntry = {
  model_name: string
  base_version: string
  definition: UnifiedModelDefinition
  legacy_parameters?: Record<string, unknown>
  reference_specifications?: OfficialReferenceRow[]
  published?: {
    version: string
    published_at: number
    definition: UnifiedModelDefinition
  }
  draft?: { base_version: string; definition: UnifiedModelDefinition }
}
export type UnifiedModelSnapshot = {
  schema_version: 1
  revision: string
  entries: UnifiedModelEntry[]
  channels: {
    id: number
    name: string
    type: number
    status: number
    groups: string[]
  }[]
  history: { model_name: string; version: string; published_at: number }[]
}

async function configurationResponse(response: {
  data: ApiResponse<UnifiedModelSnapshot>
}) {
  const value = response.data.data
  if (
    !response.data.success ||
    value?.schema_version !== 1 ||
    !Array.isArray(value.entries)
  ) {
    throw createServerError(
      response.data,
      t('Failed to load unified model configuration')
    )
  }
  return value
}

export async function getUnifiedModelConfiguration(model = '') {
  return configurationResponse(
    await api.get('/api/option/model_catalog', {
      params: model ? { model } : undefined,
    })
  )
}
export async function saveUnifiedModelDraft(
  revision: string,
  baseVersion: string,
  definition: UnifiedModelDefinition
) {
  return configurationResponse(
    await api.put('/api/option/model_catalog/draft', {
      revision,
      base_version: baseVersion,
      definition,
    })
  )
}
export async function publishUnifiedModels(
  revision: string,
  models: string[],
  rollbackVersion?: string
) {
  return configurationResponse(
    await api.post('/api/option/model_catalog/publish', {
      revision,
      models,
      rollback_version: rollbackVersion ?? '',
    })
  )
}
