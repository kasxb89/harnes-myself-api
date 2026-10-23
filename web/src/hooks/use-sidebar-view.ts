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
import { useLocation } from '@tanstack/react-router'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { resolveSidebarView } from '@/components/layout/lib/sidebar-view-registry'
import type {
  NavGroup,
  NavItem,
  ResolvedSidebarView,
} from '@/components/layout/types'
import { canAccessAdminRoute } from '@/lib/admin-permissions'
import { ROLE } from '@/lib/roles'
import { useAuthStore, type AuthUser } from '@/stores/auth-store'

import { useSidebarConfig } from './use-sidebar-config'
import { useSidebarData } from './use-sidebar-data'

/** Sentinel key used for the root navigation in animation `key=` props */
const ROOT_VIEW_KEY = '__root'

function filterAccessibleItems(
  items: NavItem[],
  user: AuthUser | null | undefined
): NavItem[] {
  const role = user?.role ?? ROLE.GUEST
  return items.flatMap((item): NavItem[] => {
    if (item.requiredRole !== undefined && role < item.requiredRole) return []
    if (item.url && !canAccessAdminRoute(user, item.url)) return []
    if (item.items) {
      const children = filterAccessibleItems(item.items, user)
      return children.length
        ? [{ ...item, items: children as typeof item.items }]
        : []
    }
    if (item.type === 'chat-presets') {
      return [
        {
          ...item,
          links: filterAccessibleItems(
            item.links ?? [],
            user
          ) as typeof item.links,
        },
      ]
    }
    return [item]
  })
}

/**
 * Resolve the active sidebar view for the current location.
 *
 * - Returns the matching nested {@link SidebarView} (with its nav
 *   groups) when the URL belongs to a registered drill-in workspace.
 * - Otherwise returns the root navigation, narrowed by:
 *     · admin-only group visibility (role-based);
 *     · `useSidebarConfig` (admin × user `sidebar_modules` overlay).
 *
 * Nested views are intentionally NOT passed through `useSidebarConfig`
 * — those filters target known dashboard URLs only, and gating is
 * already enforced at the route level (`beforeLoad` redirects).
 */
export function useSidebarView(): ResolvedSidebarView {
  const { t } = useTranslation()
  const pathname = useLocation({ select: (l) => l.pathname })
  const user = useAuthStore((s) => s.auth.user)
  const rootSidebarData = useSidebarData()
  const configFilteredRoot = useSidebarConfig(rootSidebarData.navGroups)

  const rootNavGroups = useMemo<NavGroup[]>(
    () =>
      configFilteredRoot
        .map((group) => ({
          ...group,
          items: filterAccessibleItems(group.items, user),
        }))
        .filter((group) => group.items.length > 0),
    [configFilteredRoot, user]
  )

  const view = resolveSidebarView(pathname)

  if (view) {
    return {
      key: view.id,
      view,
      navGroups: view
        .getNavGroups(t)
        .map((group) => ({
          ...group,
          items: filterAccessibleItems(group.items, user),
        }))
        .filter((group) => group.items.length > 0),
    }
  }

  return {
    key: ROOT_VIEW_KEY,
    view: null,
    navGroups: rootNavGroups,
  }
}
