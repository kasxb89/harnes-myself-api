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

const admin: AuthUser = { id: 7, username: 'operator', role: 10 }

describe('administrative route permissions', () => {
  it('denies missing capabilities and never trusts a normal user with forged grants', () => {
    expect(canAccessAdminRoute(admin, '/model-profits')).toBe(false)
    expect(
      canAccessAdminRoute(
        {
          ...admin,
          role: 1,
          permissions: { admin_permissions: { model_profits: { read: true } } },
        },
        '/model-profits'
      )
    ).toBe(false)
    expect(canAccessAdminRoute(null, '/model-profits')).toBe(false)
    expect(canAccessAdminRoute(admin, '/keys')).toBe(true)
    expect(canAccessAdminRoute(admin, '/usage-logs/audit')).toBe(true)
    expect(
      canAccessAdminRoute({ ...admin, role: 1 }, '/usage-logs/audit')
    ).toBe(true)
  })

  it('opens only the granted content section while keeping other settings restricted', () => {
    const user = {
      ...admin,
      permissions: {
        admin_permissions: { customer_service: { read: true, write: false } },
      },
    }
    expect(
      canAccessAdminRoute(user, '/system-settings/content/customer-service/')
    ).toBe(true)
    expect(canAccessAdminRoute(user, '/system-settings/content')).toBe(true)
    for (const url of [
      '/system-settings/auth',
      '/system-settings/billing',
      '/system-settings/content/announcements',
      '/system-settings/content/customer-service-unsafe',
      '/system-info',
      '/task-plugins',
    ]) {
      expect(canAccessAdminRoute(user, url), url).toBe(false)
    }
  })

  it.each(['operations', 'upstream_topups'])(
    'keeps the combined finance page reachable with only %s read access',
    (resource) => {
      const user = {
        ...admin,
        permissions: { admin_permissions: { [resource]: { read: true } } },
      }
      expect(canAccessAdminRoute(user, '/usage-logs/operations')).toBe(true)
      expect(canAccessAdminRoute(user, '/balance-ledger')).toBe(false)
    }
  )

  it.each(['media_operations', 'video_usage'])(
    'opens media management with %s read access while retaining independent legacy route permissions',
    (resource) => {
      const user = {
        ...admin,
        permissions: { admin_permissions: { [resource]: { read: true } } },
      }
      expect(canAccessAdminRoute(user, '/media-management/?type=video')).toBe(
        true
      )
      expect(canAccessAdminRoute(user, '/video-management')).toBe(true)
      expect(canAccessAdminRoute(user, '/image-management')).toBe(
        resource === 'media_operations'
      )
      expect(canAccessAdminRoute(user, '/media-operations')).toBe(
        resource === 'media_operations'
      )
      expect(canAccessAdminRoute(user, '/video-usage')).toBe(
        resource === 'video_usage'
      )
    }
  )

  it('denies media management without a granted read capability or administrator role', () => {
    expect(canAccessAdminRoute(admin, '/media-management')).toBe(false)
    expect(
      canAccessAdminRoute(
        {
          ...admin,
          permissions: {
            admin_permissions: { media_operations: { operate: true } },
          },
        },
        '/media-management'
      )
    ).toBe(false)
    expect(
      canAccessAdminRoute(
        {
          ...admin,
          role: 1,
          permissions: {
            admin_permissions: { video_usage: { read: true } },
          },
        },
        '/media-management'
      )
    ).toBe(false)
    expect(canAccessAdminRoute(null, '/media-management')).toBe(false)
    expect(
      canAccessAdminRoute({ ...admin, role: 100 }, '/media-management')
    ).toBe(true)
  })

  it('revocation is effective on the next check and super administrator access remains unchanged', () => {
    const user = {
      ...admin,
      permissions: {
        admin_permissions: { client_model_config: { read: true } },
      },
    }
    expect(canAccessAdminRoute(user, '/client-model-config')).toBe(true)
    user.permissions.admin_permissions.client_model_config.read = false
    expect(canAccessAdminRoute(user, '/client-model-config')).toBe(false)
    expect(
      canAccessAdminRoute({ ...admin, role: 100 }, '/system-settings/security')
    ).toBe(true)
  })
})
