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
  config_revision?: string
  selected_options?: SurveyOption[]
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

export interface SurveyConfigOption extends SurveyOption {
  enabled: boolean
}
export interface SurveyConfiguration {
  schema_version: 2
  survey_id: string
  config_revision: string
  options: SurveyConfigOption[]
}
export async function getSurveyConfiguration(
  signal?: AbortSignal
): Promise<SurveyConfiguration> {
  const response = await api.get<ApiResponse<SurveyConfiguration>>(
    '/api/user/onboarding-survey-config',
    { signal, disableDuplicate: true }
  )
  if (!response.data.success || !response.data.data) {
    throw createServerError(
      response.data,
      t('Failed to load questionnaire options')
    )
  }
  return response.data.data
}
export async function saveSurveyConfiguration(
  revision: string,
  options: SurveyConfigOption[]
): Promise<SurveyConfiguration> {
  const response = await api.put<ApiResponse<SurveyConfiguration>>(
    '/api/user/onboarding-survey-config',
    { schema_version: 2, expected_config_revision: revision, options }
  )
  if (!response.data.success || !response.data.data) {
    throw createServerError(
      response.data,
      t('Failed to save questionnaire options')
    )
  }
  return response.data.data
}
export function surveyOptionLabel(
  option: SurveyOption,
  language: string
): string {
  return language.startsWith('zh') ? option.label : option.label_en
}
