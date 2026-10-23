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
export type ActivityScope = 'live' | 'history'
export type ActivityTimeBasis = 'started' | 'last_seen'
export type ActivityCollection =
  | 'chat_activity'
  | 'project_run'
  | 'storyboard_task'
  | 'local_operation'
  | 'shots'
export type WorkloadCoverage = 'complete' | 'partial' | 'not_collected'
export type WorkloadObservationStatus =
  | 'live'
  | 'unknown'
  | 'ended'
  | 'expired'
  | 'not_reported'
  | 'not_collected'

export interface ActivityFilters {
  p: number
  page_size: number
  scope?: ActivityScope
  time_basis?: ActivityTimeBasis
  username?: string
  status?: 'all' | 'online' | 'foreground' | 'background' | 'offline'
  start_timestamp?: number
  end_timestamp?: number
}
export interface ClientActivitySession {
  id: number
  user_id: number
  username: string
  session_id: string
  platform: string
  app_version: string
  started_at: number
  last_seen_at: number
  ended_at: number
  online_seconds: number
  foreground_seconds: number
  foreground: boolean
  online: boolean
  status: 'foreground' | 'background' | 'offline'
  observation_detail?: 'live' | 'expired' | 'ended'
  model_calls: number
  model_errors: number
  last_model_at: number
}
export interface ClientWorkloadSummary {
  schema_version: 1
  server_time: number
  scope: 'global_live'
  known: {
    generating_chat_sessions: number
    projects: number
    planned_shots: number
    task_attempts: number
    accepted_tasks: number
    queued_tasks: number
    generating_tasks: number
    saving_tasks: number
    editing_projects: number
    local_operations: number
  }
  uncertainty: {
    unknown_task_attempts: number
    conflicting_task_attempts: number
    unknown_chat_sessions: number
    unknown_projects: number
    expired_runtime_sessions: number
    unreported_runtime_sessions: number
  }
  coverage: {
    online_runtime_sessions: number
    reporting_runtime_sessions: number
    collections: Record<
      ActivityCollection,
      {
        collected_runtime_sessions: number
        coverage: WorkloadCoverage
      }
    >
  }
}
export interface ClientActivitySummary {
  server_time: number
  heartbeat_interval_seconds: number
  online_timeout_seconds: number
  online_users: number
  foreground_users: number
  runtime_sessions?: number
  workload?: ClientWorkloadSummary
  started_sessions: number
  active_users: number
  online_seconds: number
  foreground_seconds: number
  model_calls: number
  model_errors: number
}
export interface WorkloadActivity {
  activity_id: string
  kind: Exclude<ActivityCollection, 'shots'>
  work_state: 'active' | 'unknown' | 'ended'
  phase: string
  observation_status: WorkloadObservationStatus
  chat_session_id?: string
  project_id?: string
  parent_activity_id?: string
  shot_ids?: string[]
  attempt_id?: string
  api_task_id?: string
  local_job_id?: string
  outcome?: 'succeeded' | 'failed' | 'cancelled' | 'interrupted'
}
export interface ClientWorkloadDetail {
  schema_version: 1
  observation_status: 'live' | 'expired' | 'ended' | 'not_collected'
  accepted_at: number
  accepted_sequence: number
  collections: Record<ActivityCollection, 'collected' | 'not_collected'>
  activities: WorkloadActivity[]
  shots: {
    project_id: string
    shot_id: string
    observation_status: 'live' | 'expired' | 'not_reported'
  }[]
  truncated: boolean
}
export interface ClientActivityDetail {
  session: ClientActivitySession
  workload?: ClientWorkloadDetail
  usage: {
    calls: number
    errors: number
    models: {
      model_name: string
      calls: number
      errors: number
      last_called_at: number
    }[]
  }
}
