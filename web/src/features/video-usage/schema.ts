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
const seconds = z.number().nonnegative().nullable()
export const clientSources = ['desktop', 'unknown'] as const
const source = z.enum(clientSources)
const current = z.object({
  not_start: count,
  submitted: count,
  queued: count,
  in_progress: count,
  unknown: count,
  inflight: count,
  total_records: count,
})
const duration = z.object({
  mean_seconds: seconds,
  p50_seconds: seconds,
  p95_seconds: seconds,
  valid_samples: count,
  missing_samples: count,
  invalid_samples: count,
  zero_samples: count,
})
const peak = z.object({
  value: count.nullable(),
  interval_samples: count,
  excluded_intervals: count,
  unknown_state: count,
  invalid_timestamps: count,
  missing_finish: count,
  zero_length: count,
})
const history = z.object({
  task_count: count,
  success: count,
  failure: count,
  unresolved: count,
  users: count,
  turnaround: duration,
  recorded_run: duration,
})
const user = z.object({
  id: z.number().int().positive(),
  username: z.string().max(64),
  display_name: z.string().max(255),
})
export const videoTaskSchema = z
  .object({
    id: z.number().int().positive(),
    task_id: z.string().max(128).nullable(),
    model: z.string().max(255),
    user: user.nullable(),
    client_source: source,
    status: z.string().max(32),
    timing_quality: z.enum(['valid', 'missing', 'invalid']),
    submitted_at: count.nullable(),
    started_at: count.nullable(),
    finished_at: count.nullable(),
    turnaround_seconds: seconds,
    recorded_running_seconds: seconds,
    age_seconds: seconds,
    waiting_seconds: seconds,
    reported_video_seconds: seconds,
    reported_video_duration_source: z.enum(['h3_usage', 'unknown']),
  })
  .refine(
    (task) =>
      task.reported_video_duration_source === 'h3_usage' ||
      task.reported_video_seconds === null
  )
export const videoUsageSchema = z.object({
  schema_version: z.literal(1),
  as_of: count,
  start_timestamp: count,
  end_timestamp: count,
  observation_complete: z.literal(false),
  limitations: z.array(z.string().max(128)).max(32),
  summary: z.object({ current, history, peak }),
  source_coverage: z.object({ total: count, desktop: count, unknown: count }),
  client_session_duration: z.object({
    status: z.literal('not_collected'),
    seconds: z.null(),
  }),
  upstream_capacity: z.object({
    status: z.literal('unknown'),
    configured_limit: z.null(),
  }),
  models: z
    .array(z.object({ model: z.string().max(255), current, history, peak }))
    .max(5000),
  filter_options: z.object({
    models: z.array(z.string().max(255)).max(5000),
    users: z.array(user).max(10000),
  }),
  tasks: z.object({
    scope: z.enum(['history', 'current']),
    page: z.number().int().positive(),
    page_size: z.number().int().min(1).max(100),
    total: count,
    items: z.array(videoTaskSchema).max(100),
  }),
})
export type VideoUsageResult = z.infer<typeof videoUsageSchema>
export type VideoTask = z.infer<typeof videoTaskSchema>
export type VideoUser = z.infer<typeof user>
export type VideoDuration = z.infer<typeof duration>
export type VideoPeak = z.infer<typeof peak>
export type VideoUsageQuery = {
  start_timestamp: number
  end_timestamp: number
  model?: string
  user_id?: number
  client_source: 'all' | (typeof clientSources)[number]
  task_scope: 'history' | 'current'
  p: number
  page_size: number
}
