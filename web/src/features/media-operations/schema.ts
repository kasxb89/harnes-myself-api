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
const currentStates = z.object({
  not_start: count,
  submitted: count,
  queued: count,
  in_progress: count,
  unknown: count,
})
const historyCounts = z.object({
  accepted_tasks: count,
  error_events: count,
  error_requests: count,
  submit_error_requests: count,
  terminal_error_requests: count,
  unknown_stage_error_requests: count,
  observed_policy_attempts: count,
  client_report_events: count,
  correlated_client_requests: count,
})
export const mediaErrorCategories = [
  'content_policy',
  'generation_capacity',
  'model_unavailable',
  'invalid_request',
  'rate_limit',
  'concurrency_limit',
  'timeout',
  'transport',
  'unknown',
  'other',
] as const
export const mediaErrorStages = [
  'submit',
  'task_poll',
  'task_terminal',
  'task_timeout',
  'preflight',
  'query',
  'local-save',
  'unknown',
] as const
export const mediaOperationsSchema = z.object({
  schema_version: z.literal(1),
  as_of: count,
  history_start: count,
  history_end: count,
  source: z.literal('persisted_gateway_observation'),
  observation_complete: z.literal(false),
  limitations: z.array(z.string().max(128)).max(32),
  summary: currentStates,
  models: z
    .array(
      z.object({
        model: z.string().max(255),
        media_kind: z.enum(['image', 'video', 'unknown']),
        capacity: z.object({
          status: z.literal('unknown'),
          configured_limit: z.null(),
          available_slots: z.null(),
        }),
        tasks: currentStates.extend({ success: count, failure: count }),
        history: historyCounts,
        poll: z.object({
          tasks_with_failures: count,
          max_consecutive_failures: count,
        }),
      })
    )
    .max(5000),
  history_summary: historyCounts.extend({
    categories: z
      .array(
        z.object({
          category: z.enum(mediaErrorCategories),
          events: count,
          requests: count,
        })
      )
      .max(mediaErrorCategories.length),
    stages: z
      .array(
        z.object({
          source: z.enum(['gateway', 'client']),
          stage: z.enum(mediaErrorStages),
          events: count,
          requests: count,
        })
      )
      .max(mediaErrorStages.length * 2),
  }),
})

export type MediaOperationsResult = z.infer<typeof mediaOperationsSchema>
export type MediaOperationModel = MediaOperationsResult['models'][number]
export type MediaErrorCategory = (typeof mediaErrorCategories)[number]
export type MediaErrorStage = (typeof mediaErrorStages)[number]
export interface MediaOperationsQuery {
  start_timestamp: number
  end_timestamp: number
  media_kind: 'all' | 'image' | 'video'
  model?: string
}
