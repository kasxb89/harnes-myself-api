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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { canAccessAdminRoute, hasPermission } from '@/lib/admin-permissions'
import { useAuthStore } from '@/stores/auth-store'

import {
  ANNOUNCEMENTS_QUERY_KEY,
  loadAnnouncements,
  saveAnnouncements,
} from './announcements-api'
import { AnnouncementsSection } from './announcements-section'

export function AnnouncementsSettingsSection() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const allowed = canAccessAdminRoute(
    user,
    '/system-settings/content/announcements'
  )
  const canEdit = allowed && hasPermission(user, 'announcements', 'write')
  const client = useQueryClient()
  const query = useQuery({
    queryKey: ANNOUNCEMENTS_QUERY_KEY,
    queryFn: ({ signal }) => loadAnnouncements(signal),
    enabled: allowed,
    meta: { errorToast: false },
  })
  const save = useMutation({
    mutationFn: saveAnnouncements,
    meta: { errorToast: false },
    onMutate: () => client.cancelQueries({ queryKey: ANNOUNCEMENTS_QUERY_KEY }),
    onSuccess: (data) => {
      client.setQueryData(ANNOUNCEMENTS_QUERY_KEY, data)
      void client.invalidateQueries({ queryKey: ['status'] })
      void client.invalidateQueries({ queryKey: ANNOUNCEMENTS_QUERY_KEY })
    },
  })
  if (!allowed) return <ErrorState title={t('Access denied')} />
  if (query.isPending) {
    return <LoadingState message={t('Loading content settings...')} />
  }
  if (!query.data) {
    return (
      <ErrorState
        title={t('Failed to load settings')}
        onRetry={() => void query.refetch()}
      />
    )
  }
  return (
    <AnnouncementsSection
      data={query.data['console_setting.announcements']}
      enabled={query.data['console_setting.announcements_enabled'] === 'true'}
      readOnly={!canEdit}
      saveSettings={async (data, enabled) => {
        if (!canEdit) throw new Error(t('Access denied'))
        const saved = await save.mutateAsync({
          'console_setting.announcements': data,
          'console_setting.announcements_enabled': enabled ? 'true' : 'false',
        })
        return {
          data: saved['console_setting.announcements'],
          enabled: saved['console_setting.announcements_enabled'] === 'true',
        }
      }}
    />
  )
}
