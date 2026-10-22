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
import type { MediaErrorCategory, MediaErrorStage } from './schema'

export const categoryLabels: Record<MediaErrorCategory, string> = {
  content_policy: 'Content policy',
  generation_capacity: 'Upstream generation resources',
  model_unavailable: 'Model unavailable',
  invalid_request: 'Invalid parameters',
  rate_limit: 'Rate limit',
  concurrency_limit: 'Concurrency limit',
  timeout: 'Timeout',
  transport: 'Transport or network',
  unknown: 'Cause unknown',
  other: 'Other observed errors',
}
export const stageLabels: Record<MediaErrorStage, string> = {
  submit: 'Submission',
  task_poll: 'Task polling',
  task_terminal: 'Task terminal result',
  task_timeout: 'Task timeout',
  preflight: 'Preflight',
  query: 'Query',
  'local-save': 'Local save',
  unknown: 'Stage unknown',
}
