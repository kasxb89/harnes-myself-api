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

export type SurveyStatus = 'pending' | 'deferred' | 'submitted'
export interface SurveyOption {
  id: string
  label: string
  label_en: string
}
export interface SurveyRegistration {
  user_id: number
  phone_e164: string
  status: SurveyStatus
  selected_option_ids: string[]
  notes: string
  registered_at: number
  submitted_at: number
  deferred_at: number
}
export interface SurveyFilters {
  p: number
  page_size: number
  phone?: string
  status?: SurveyStatus
  option_id?: string
}
export interface SurveyRegistrations {
  schema_version: number
  survey_id: string
  options: SurveyOption[]
  items: SurveyRegistration[]
  total: number
}
export async function getSurveyRegistrations(
  params: SurveyFilters,
  signal?: AbortSignal
) {
  const response = await api.get<ApiResponse<SurveyRegistrations>>(
    '/api/user/onboarding-surveys',
    { params, signal, disableDuplicate: true }
  )
  if (!response.data.success || !response.data.data) {
    throw createServerError(response.data, t('Failed to load questionnaires'))
  }
  return response.data.data
}
