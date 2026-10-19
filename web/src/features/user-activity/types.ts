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
export interface ActivityFilters {
  p: number
  page_size: number
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
  model_calls: number
  model_errors: number
  last_model_at: number
}
export interface ClientActivitySummary {
  server_time: number
  heartbeat_interval_seconds: number
  online_timeout_seconds: number
  online_users: number
  foreground_users: number
  started_sessions: number
  active_users: number
  online_seconds: number
  foreground_seconds: number
  model_calls: number
  model_errors: number
}
export interface ClientActivityDetail {
  session: ClientActivitySession
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
