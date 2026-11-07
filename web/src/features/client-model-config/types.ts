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
export type ClientModelConfig = {
  schema_version: 1
  default_chat_model: string
  model_order: string[]
}

export type ClientModelOption = {
  id: string
  display_name: string
  chat_eligible: boolean
  image_eligible?: boolean
  video_eligible?: boolean
}

export type ClientModelPresentationEntry = {
  display_name?: string
  description?: string
  badge?: string
}

export type ClientModelPresentation = {
  schema_version: 1
  models: Record<string, ClientModelPresentationEntry>
}

export type ClientMediaDefaults = {
  schema_version: 1
  default_image_model: string
  default_video_model: string
}

export type ClientModelCapacityRoute = {
  model: string
  group: string
  channel_id: number
  channel_name: string
  route_model: string
  context_window?: number
  max_output_tokens?: number
  budget_field: string
  source: string
  verified_at: string
  configured: boolean
  valid: boolean
  requires_full_capacity?: boolean
}

export type ClientModelCapacityCatalog = {
  revision: string
  routes: ClientModelCapacityRoute[]
}

export type ClientModelCapacityChange = Pick<
  ClientModelCapacityRoute,
  | 'model'
  | 'group'
  | 'channel_id'
  | 'route_model'
  | 'budget_field'
  | 'source'
  | 'verified_at'
> & { context_window?: number; max_output_tokens?: number }
