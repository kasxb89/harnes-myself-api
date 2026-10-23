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
import { createFileRoute, redirect } from '@tanstack/react-router'

import {
  CONTENT_DEFAULT_SECTION,
  CONTENT_SECTION_IDS,
} from '@/features/system-settings/content/section-registry.tsx'
import { canAccessAdminRoute } from '@/lib/admin-permissions'
import { useAuthStore } from '@/stores/auth-store'

export const Route = createFileRoute(
  '/_authenticated/system-settings/content/'
)({
  beforeLoad: ({ location }) => {
    const user = useAuthStore.getState().auth.user
    const requested = new URLSearchParams(location.searchStr).get('section')
    const preferred =
      requested &&
      (CONTENT_SECTION_IDS as readonly string[]).includes(requested)
        ? requested
        : CONTENT_DEFAULT_SECTION
    if (
      requested &&
      (CONTENT_SECTION_IDS as readonly string[]).includes(requested) &&
      !canAccessAdminRoute(user, `/system-settings/content/${requested}`)
    ) {
      throw redirect({ to: '/403' })
    }
    const section = canAccessAdminRoute(
      user,
      `/system-settings/content/${preferred}`
    )
      ? preferred
      : ['customer-service', 'announcements'].find((item) =>
          canAccessAdminRoute(user, `/system-settings/content/${item}`)
        )
    if (!section) throw redirect({ to: '/403' })
    throw redirect({
      to: '/system-settings/content/$section',
      params: { section },
    })
  },
})
