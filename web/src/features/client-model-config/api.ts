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

import type { ClientModelConfig, ClientModelOption } from './types'

export async function getClientModelConfig(): Promise<ClientModelConfig> {
  const response = await api.get<ApiResponse<ClientModelConfig>>(
    '/api/client-model-config'
  )
  if (!response.data.success || !response.data.data) {
    throw createServerError(
      response.data,
      t('Failed to load client model configuration')
    )
  }
  return response.data.data
}

export async function getClientModelCatalog(): Promise<ClientModelOption[]> {
  const response = await api.get<ApiResponse<{ models: ClientModelOption[] }>>(
    '/api/client-model-config/models'
  )
  if (!response.data.success || !Array.isArray(response.data.data?.models)) {
    throw createServerError(
      response.data,
      t('Failed to load client model catalog')
    )
  }
  return response.data.data.models
}

export async function saveClientModelConfig(
  config: ClientModelConfig
): Promise<ClientModelConfig> {
  const response = await api.put<ApiResponse<ClientModelConfig>>(
    '/api/client-model-config',
    config
  )
  if (!response.data.success || !response.data.data) {
    throw createServerError(
      response.data,
      t('Failed to save client model configuration')
    )
  }
  return response.data.data
}
