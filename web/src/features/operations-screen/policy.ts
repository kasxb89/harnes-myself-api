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

import { hasPermission } from '@/lib/admin-permissions'
import { ROLE } from '@/lib/roles'
import type { AuthUser } from '@/stores/auth-store'

export const operationsTabs = [
  { value: 'profit', labelKey: 'Profit', resource: 'model_profits' },
  {
    value: 'concurrency',
    labelKey: 'Concurrency',
    resource: 'media_operations',
  },
  { value: 'usage', labelKey: 'Call usage', resource: 'diagnostics' },
  { value: 'errors', labelKey: 'API errors', resource: 'diagnostics' },
  { value: 'image', labelKey: 'Image', resource: 'media_operations' },
  { value: 'video', labelKey: 'Video', resource: 'media_operations' },
  { value: 'activity', labelKey: 'User activity', resource: 'user_activity' },
] as const
export type OperationsTab = (typeof operationsTabs)[number]['value']
export type OperationsRange = '5m' | '1h' | '24h'
export interface OperationsWindow {
  start_timestamp: number
  end_timestamp: number
}
export const operationsSearchSchema = z.object({
  tab: z
    .enum([
      'profit',
      'concurrency',
      'usage',
      'errors',
      'image',
      'video',
      'media',
      'activity',
    ])
    .optional()
    .catch(undefined),
})

export function availableOperationsTabs(
  user: AuthUser | null | undefined
): OperationsTab[] {
  if (!user || user.role < ROLE.ADMIN) return []
  return operationsTabs
    .filter((tab) => hasPermission(user, tab.resource, 'read'))
    .map((tab) => tab.value)
}
export function resolveOperationsTab(
  requested: unknown,
  user: AuthUser | null | undefined
): OperationsTab | undefined {
  const available = availableOperationsTabs(user)
  const compatible = requested === 'media' ? 'video' : requested
  return available.find((tab) => tab === compatible) ?? available[0]
}
export function operationsScope(
  user: AuthUser | null | undefined,
  sid: string | null | undefined
): string {
  return JSON.stringify([
    user?.id,
    sid ?? null,
    user?.role,
    user?.permissions?.admin_permissions ?? null,
  ])
}
export function operationsWindow(range: OperationsRange): OperationsWindow {
  const seconds = { '5m': 300, '1h': 3600, '24h': 86400 }[range]
  const end = Math.floor(Date.now() / 1000)
  return { start_timestamp: end - seconds, end_timestamp: end }
}

export function supportsOperationsModelFilter(tab: OperationsTab): boolean {
  return tab !== 'concurrency' && tab !== 'activity'
}

export function validOperationsFilters(
  tab: OperationsTab,
  window: OperationsWindow,
  modelName?: string
): boolean {
  const { start_timestamp: start, end_timestamp: end } = window
  const maxDays =
    tab === 'image' || tab === 'video' || tab === 'activity' ? 31 : 366
  if (
    !Number.isSafeInteger(start) ||
    !Number.isSafeInteger(end) ||
    start < 0 ||
    end <= start ||
    end > Math.floor(Date.now() / 1000) ||
    end - start > maxDays * 86400
  ) {
    return false
  }
  return validOperationsModelName(modelName)
}

export function validOperationsModelName(modelName?: string): boolean {
  return (
    !modelName ||
    (new TextEncoder().encode(modelName).length <= 255 &&
      !/\p{Cc}/u.test(modelName) &&
      modelName.trim() === modelName)
  )
}
