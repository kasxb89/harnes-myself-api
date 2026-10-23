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

import type { ApiResponse } from '@/features/profile/types'
import { api } from '@/lib/api'
import { createServerError } from '@/lib/server-error-message'

import type { CostRules, ModelProfitResult, ProfitFilters } from './types'

export async function getModelProfits(
  params: ProfitFilters,
  signal?: AbortSignal
): Promise<ModelProfitResult> {
  const response = await api.get<ApiResponse<ModelProfitResult>>(
    '/api/model-profit/',
    { params, signal, disableDuplicate: true }
  )
  if (!response.data.success || !response.data.data) {
    throw createServerError(response.data, t('Failed to load model profits'))
  }
  return response.data.data
}

export async function getCostRules(signal?: AbortSignal): Promise<CostRules> {
  const response = await api.get<ApiResponse<CostRules>>(
    '/api/model-profit/cost-rules',
    { signal, disableDuplicate: true }
  )
  if (!response.data.success || !response.data.data) {
    throw createServerError(response.data, t('Failed to load upstream costs'))
  }
  return response.data.data
}

export async function saveCostRules(rules: CostRules): Promise<CostRules> {
  const response = await api.put<ApiResponse<CostRules>>(
    '/api/model-profit/cost-rules',
    rules
  )
  if (!response.data.success || !response.data.data) {
    throw createServerError(response.data, t('Failed to save upstream costs'))
  }
  return response.data.data
}
