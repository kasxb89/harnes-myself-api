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
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'

import { SettingsSwitchField } from '../components/settings-form-layout'
import {
  loadAnnouncementTopBanner,
  saveAnnouncementTopBanner,
} from './announcements-api'

export function AnnouncementTopBannerSwitch(props: { readOnly: boolean }) {
  const { t } = useTranslation()
  const cache = useQueryClient()
  const query = useQuery({
    queryKey: ['announcement-top-banner'],
    queryFn: loadAnnouncementTopBanner,
  })
  const mutation = useMutation({
    mutationFn: saveAnnouncementTopBanner,
    onSuccess: (value) => {
      cache.setQueryData(['announcement-top-banner'], value)
      toast.success(t('Setting saved'))
    },
  })
  if (query.isPending) return <LoadingState />
  if (!query.data) {
    return (
      <ErrorState
        title={t('Failed to load settings')}
        onRetry={() => void query.refetch()}
      />
    )
  }
  return (
    <SettingsSwitchField
      controlId='client-announcement-top-banner'
      label={t('Show announcements at the top of the client')}
      description={t(
        'Rotate published announcements at the top. Users can close the current version; updated content can appear again. The announcement master switch must also be enabled.'
      )}
      checked={query.data.enabled}
      onCheckedChange={(enabled) =>
        mutation.mutate({ schema_version: 1, enabled })
      }
      disabled={props.readOnly || mutation.isPending}
    />
  )
}
