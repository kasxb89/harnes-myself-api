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
import type { MediaOperationsResult } from '../schema'

const now = Math.floor(Date.now() / 1000)
export const mediaPayload: MediaOperationsResult = {
  schema_version: 1,
  as_of: now,
  history_start: now - 86400,
  history_end: now,
  source: 'persisted_gateway_observation',
  observation_complete: false,
  limitations: ['capacity_not_configured'],
  summary: {
    not_start: 1,
    submitted: 3,
    queued: 2,
    in_progress: 4,
    unknown: 5,
  },
  models: [
    {
      model: 'MiniMax-H3',
      media_kind: 'video',
      capacity: {
        status: 'unknown',
        configured_limit: null,
        available_slots: null,
      },
      tasks: {
        not_start: 1,
        submitted: 3,
        queued: 2,
        in_progress: 4,
        unknown: 5,
        success: 7,
        failure: 8,
      },
      history: {
        accepted_tasks: 19,
        error_events: 8,
        error_requests: 2,
        submit_error_requests: 1,
        terminal_error_requests: 1,
        unknown_stage_error_requests: 0,
        observed_policy_attempts: 3,
        client_report_events: 4,
        correlated_client_requests: 1,
      },
      poll: { tasks_with_failures: 1, max_consecutive_failures: 2 },
    },
    {
      model: 'yunshu-internal-gpt-image-2',
      media_kind: 'image',
      capacity: {
        status: 'unknown',
        configured_limit: null,
        available_slots: null,
      },
      tasks: {
        not_start: 0,
        submitted: 0,
        queued: 0,
        in_progress: 0,
        unknown: 0,
        success: 0,
        failure: 0,
      },
      history: {
        accepted_tasks: 0,
        error_events: 0,
        error_requests: 0,
        submit_error_requests: 0,
        terminal_error_requests: 0,
        unknown_stage_error_requests: 0,
        observed_policy_attempts: 0,
        client_report_events: 0,
        correlated_client_requests: 0,
      },
      poll: { tasks_with_failures: 0, max_consecutive_failures: 0 },
    },
  ],
  history_summary: {
    accepted_tasks: 19,
    error_events: 8,
    error_requests: 2,
    submit_error_requests: 1,
    terminal_error_requests: 1,
    unknown_stage_error_requests: 0,
    observed_policy_attempts: 3,
    client_report_events: 4,
    correlated_client_requests: 1,
    categories: [
      { category: 'content_policy', events: 6, requests: 1 },
      { category: 'generation_capacity', events: 2, requests: 1 },
    ],
    stages: [
      { source: 'gateway', stage: 'task_terminal', events: 8, requests: 2 },
      { source: 'client', stage: 'preflight', events: 4, requests: 1 },
    ],
  },
}
export const emptyMediaPayload: MediaOperationsResult = {
  ...mediaPayload,
  summary: {
    not_start: 0,
    submitted: 0,
    queued: 0,
    in_progress: 0,
    unknown: 0,
  },
  models: [],
  history_summary: {
    accepted_tasks: 0,
    error_events: 0,
    error_requests: 0,
    submit_error_requests: 0,
    terminal_error_requests: 0,
    unknown_stage_error_requests: 0,
    observed_policy_attempts: 0,
    client_report_events: 0,
    correlated_client_requests: 0,
    categories: [],
    stages: [],
  },
}
