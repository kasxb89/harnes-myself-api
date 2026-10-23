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
import { describe, expect, it } from 'vitest'

import { canAccessAdminRoute } from '@/lib/admin-permissions'
import type { AuthUser } from '@/stores/auth-store'

import {
  availableOperationsTabs,
  resolveOperationsTab,
  validOperationsFilters,
} from '../policy'

const admin: AuthUser = { id: 7, username: 'operator', role: 10 }

describe('operations screen permission isolation', () => {
  it('denies entry without an independent read grant, including forged ordinary-user capabilities', () => {
    expect(canAccessAdminRoute(admin, '/operations-screen')).toBe(false)
    expect(availableOperationsTabs(admin)).toEqual([])
    expect(
      availableOperationsTabs({
        ...admin,
        role: 1,
        permissions: {
          admin_permissions: {
            diagnostics: { read: true },
            media_operations: { read: true },
          },
        },
      })
    ).toEqual([])
  })
  it.each([
    ['model_profits', ['profit']],
    ['media_operations', ['concurrency', 'image', 'video']],
    ['diagnostics', ['usage', 'errors']],
    ['user_activity', ['activity']],
  ] as const)(
    'opens only the permitted tabs with %s read access',
    (resource, tabs) => {
      const user = {
        ...admin,
        permissions: { admin_permissions: { [resource]: { read: true } } },
      }
      expect(canAccessAdminRoute(user, '/operations-screen')).toBe(true)
      expect(availableOperationsTabs(user)).toEqual(tabs)
      expect(resolveOperationsTab('profit', user)).toBe(tabs[0])
      expect(resolveOperationsTab('invalid', user)).toBe(tabs[0])
    }
  )
  it('falls back after revocation and does not treat operate as read', () => {
    const user = {
      ...admin,
      permissions: {
        admin_permissions: {
          media_operations: { operate: true },
          user_activity: { read: true },
        },
      },
    }
    expect(resolveOperationsTab('concurrency', user)).toBe('activity')
    expect(resolveOperationsTab('activity', admin)).toBeUndefined()
  })
})

it('keeps old media links usable while resolving them to the distinct video section', () => {
  expect(resolveOperationsTab('media', { ...admin, role: 100 })).toBe('video')
})

it('accepts older profit and call history while respecting the task and activity 31-day server limits', () => {
  const end = Math.floor(Date.now() / 1000)
  const long = { start_timestamp: end - 365 * 86400, end_timestamp: end }
  expect(validOperationsFilters('profit', long)).toBe(true)
  expect(validOperationsFilters('errors', long)).toBe(true)
  expect(validOperationsFilters('image', long)).toBe(false)
  expect(validOperationsFilters('video', long)).toBe(false)
  expect(validOperationsFilters('activity', long)).toBe(false)
  expect(
    validOperationsFilters('video', {
      start_timestamp: end - 31 * 86400,
      end_timestamp: end,
    })
  ).toBe(true)
})
