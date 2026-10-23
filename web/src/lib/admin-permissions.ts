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
import type { AuthUser } from '@/stores/auth-store'

import { ROLE } from './roles'

export type AdminPermissionMatrix = Record<string, Record<string, boolean>>
export type AdminCapabilities = AdminPermissionMatrix

export const ADMIN_PERMISSION_RESOURCES = {
  AUDIT: 'audit',
  CHANNEL: 'channel',
  TASK_PLUGIN: 'task_plugin',
  OPERATIONS: 'operations',
  MODEL_PROFITS: 'model_profits',
  BALANCE_LEDGER: 'balance_ledger',
  UPSTREAM_TOPUPS: 'upstream_topups',
  USER_ACTIVITY: 'user_activity',
  VIDEO_USAGE: 'video_usage',
  MEDIA_OPERATIONS: 'media_operations',
  CLIENT_MODEL_CONFIG: 'client_model_config',
  CREATION_CONTENT: 'creation_content',
  CUSTOMER_SERVICE: 'customer_service',
  ANNOUNCEMENTS: 'announcements',
  DIAGNOSTICS: 'diagnostics',
} as const

export const ADMIN_PERMISSION_ACTIONS = {
  READ: 'read',
  OPERATE: 'operate',
  WRITE: 'write',
  SENSITIVE_WRITE: 'sensitive_write',
  SECRET_VIEW: 'secret_view',
  BIND: 'bind',
} as const

// The role whose baseline grants are used as defaults in the permission editor.
export const ADMIN_ROLE_KEY = 'admin'

// The permission catalog (resources, actions, labels and role baselines) is owned
// by the backend authz package and fetched from GET /api/authz/catalog. It is
// intentionally NOT duplicated here so the schema stays defined in one place.
// These types mirror the backend JSON shape.
export interface PermissionActionDef {
  action: string
  label_key: string
  description_key: string
  root_only?: boolean
}

export interface PermissionResourceDef {
  resource: string
  label_key: string
  group_key?: string
  description_key?: string
  session_only?: boolean
  actions: PermissionActionDef[]
}

export interface PermissionRoleDef {
  key: string
  name: string
  built_in: boolean
  superuser: boolean
  grants: AdminPermissionMatrix
}

export interface PermissionCatalog {
  resources: PermissionResourceDef[]
  roles: PermissionRoleDef[]
}

export const EMPTY_PERMISSION_CATALOG: PermissionCatalog = {
  resources: [],
  roles: [],
}

export function hasPermission(
  user: AuthUser | null | undefined,
  resource: string,
  action: string
): boolean {
  if (!user) return false
  if (user.role === ROLE.SUPER_ADMIN) return true
  return user.permissions?.admin_permissions?.[resource]?.[action] === true
}

// A single navigation/page policy keeps direct links and nested menus aligned.
// Every privileged API independently checks its corresponding permission.
export function canAccessAdminRoute(
  user: AuthUser | null | undefined,
  pathname: string
): boolean {
  if (!user) return false
  if (user.role === ROLE.SUPER_ADMIN) return true
  const path = pathname.split(/[?#]/, 1)[0].replace(/\/+$/, '') || '/'
  const matches = (prefix: string) =>
    path === prefix || path.startsWith(`${prefix}/`)
  const read = (resource: string) =>
    user.role >= ROLE.ADMIN && hasPermission(user, resource, 'read')

  if (matches('/system-settings')) {
    if (matches('/system-settings/content/customer-service')) {
      return read('customer_service')
    }
    if (matches('/system-settings/content/announcements')) {
      return read('announcements')
    }
    if (path === '/system-settings' || path === '/system-settings/content') {
      return read('customer_service') || read('announcements')
    }
    return false
  }
  if (matches('/system-info') || matches('/task-plugins')) return false
  if (matches('/usage-logs/operations')) {
    return read('operations') || read('upstream_topups')
  }
  if (matches('/operations-screen')) {
    return [
      'model_profits',
      'media_operations',
      'diagnostics',
      'user_activity',
    ].some(read)
  }
  if (matches('/media-management') || matches('/video-management')) {
    return read('media_operations') || read('video_usage')
  }
  const resources: ReadonlyArray<readonly [string, string]> = [
    ['/model-profits', 'model_profits'],
    ['/dashboard/users', 'operations'],
    ['/balance-ledger', 'balance_ledger'],
    ['/upstream-topups', 'upstream_topups'],
    ['/user-activity', 'user_activity'],
    ['/video-usage', 'video_usage'],
    ['/media-operations', 'media_operations'],
    ['/image-management', 'media_operations'],
    ['/client-model-config', 'client_model_config'],
    ['/creation-content', 'creation_content'],
    ['/usage-logs/errors', 'diagnostics'],
    ['/client-errors', 'diagnostics'],
    ['/channels', 'channel'],
  ]
  for (const [prefix, resource] of resources) {
    if (matches(prefix)) return read(resource)
  }
  // The audit page narrows to /api/audit/self when global audit access is absent.
  for (const prefix of [
    '/users',
    '/models',
    '/redemption-codes',
    '/subscriptions',
  ]) {
    if (matches(prefix)) return user.role >= ROLE.ADMIN
  }
  return true
}

// roleGrants returns the baseline grant matrix for the given role key.
export function roleGrants(
  catalog: PermissionCatalog,
  roleKey: string
): AdminPermissionMatrix {
  return catalog.roles.find((role) => role.key === roleKey)?.grants ?? {}
}

// normalizeAdminPermissions produces a full matrix for the catalog, filling any
// value missing from `value` with the admin role's baseline grant.
export function normalizeAdminPermissions(
  value: AdminPermissionMatrix | null | undefined,
  catalog: PermissionCatalog
): AdminPermissionMatrix {
  const baseline = roleGrants(catalog, ADMIN_ROLE_KEY)
  const normalized: AdminPermissionMatrix = {}
  for (const resource of catalog.resources) {
    const actions: Record<string, boolean> = {}
    for (const action of resource.actions) {
      actions[action.action] =
        value?.[resource.resource]?.[action.action] ??
        baseline[resource.resource]?.[action.action] ??
        false
    }
    normalized[resource.resource] = actions
  }
  return normalized
}

// permissionMatrixToScopes lists the granted cells of a matrix as sorted
// `resource:action` scope keys.
export function permissionMatrixToScopes(
  matrix: AdminPermissionMatrix
): string[] {
  const scopes: string[] = []
  for (const [resource, actions] of Object.entries(matrix)) {
    for (const [action, granted] of Object.entries(actions)) {
      if (granted) scopes.push(`${resource}:${action}`)
    }
  }
  return scopes.sort()
}

// scopesToPermissionMatrix is the inverse of permissionMatrixToScopes; keys
// without a separator are ignored.
export function scopesToPermissionMatrix(
  scopes: readonly string[]
): AdminPermissionMatrix {
  const matrix: AdminPermissionMatrix = {}
  for (const scope of scopes) {
    const separator = scope.indexOf(':')
    if (separator <= 0) continue
    const resource = scope.slice(0, separator)
    const action = scope.slice(separator + 1)
    matrix[resource] = { ...matrix[resource], [action]: true }
  }
  return matrix
}
