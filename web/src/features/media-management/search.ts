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

export const mediaManagementSearchSchema = z.object({
  tab: z.enum(['tasks', 'concurrency', 'video']).optional().catch(undefined),
})

export type MediaManagementTab = 'tasks' | 'concurrency' | 'video'

export function resolveMediaManagementTab(
  requested: unknown,
  canReadMedia: boolean,
  canReadVideo: boolean
): MediaManagementTab {
  if (canReadMedia && (requested === 'tasks' || requested === 'concurrency')) {
    return requested
  }
  if (canReadVideo && requested === 'video') {
    return 'video'
  }
  return canReadMedia ? 'tasks' : 'video'
}

export const generationManagementSearchSchema = z.object({
  tab: z
    .enum(['tasks', 'concurrency', 'statistics'])
    .optional()
    .catch(undefined),
})
