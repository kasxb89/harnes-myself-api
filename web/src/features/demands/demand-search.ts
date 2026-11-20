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

const tabSchema = z.enum([
  'demands',
  'community',
  'service-profiles',
  'customer-profiles',
  'engineers',
  'settings',
  'email',
])
const idSchema = z
  .string()
  .min(1)
  .max(100)
  .regex(/^[a-zA-Z0-9_-]+$/)
export type DemandManagementSearch = {
  tab?: z.infer<typeof tabSchema>
  demandId?: string
  orderId?: string
  invalidLink?: boolean
}
export function parseDemandManagementSearch(
  search: Record<string, unknown>
): DemandManagementSearch {
  const parsed = z
    .object({
      tab: tabSchema.optional(),
      demandId: idSchema.optional(),
      orderId: idSchema.optional(),
    })
    .safeParse(search)
  if (!parsed.success) return { invalidLink: true }
  const value = parsed.data
  if (
    (value.demandId && value.orderId) ||
    (value.demandId && value.tab && value.tab !== 'demands') ||
    (value.orderId && value.tab && value.tab !== 'community')
  ) {
    return { invalidLink: true }
  }
  let tab = value.tab
  if (!tab && value.demandId) tab = 'demands'
  if (!tab && value.orderId) tab = 'community'
  return { ...value, tab }
}
