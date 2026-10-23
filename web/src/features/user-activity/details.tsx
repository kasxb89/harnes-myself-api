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
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import {
  DetailRow,
  DetailSection,
} from '@/features/usage-logs/components/dialogs/log-detail-layout'
import { toIntlLocale } from '@/i18n/languages'
import { canAccessAdminRoute } from '@/lib/admin-permissions'
import { formatNumber } from '@/lib/format'
import { useAuthStore } from '@/stores/auth-store'

import { getActivityDetail } from './api'
import { ActivityDuration, ActivityStatus, ActivityTimestamp } from './display'

export function ActivityDetails(props: {
  id: number | null
  onClose: () => void
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const user = useAuthStore((state) => state.auth.user)
  const allowed = canAccessAdminRoute(user, '/user-activity')
  const query = useQuery({
    queryKey: ['client-activity', user?.id, 'detail', props.id],
    queryFn: () => {
      if (props.id === null) {
        throw new Error(t('Client activity session not found'))
      }
      return getActivityDetail(props.id)
    },
    enabled: allowed && props.id !== null,
    retry: false,
    refetchInterval: 30_000,
  })
  const data = allowed && !query.isError ? query.data : undefined
  const session = data?.session
  const events = session
    ? [
        {
          key: 'launch',
          label: t('Client session reporting started'),
          time: session.started_at,
        },
        {
          key: 'heartbeat',
          label: t('Last heartbeat observed'),
          time: session.last_seen_at,
        },
        {
          key: 'end',
          label: t('Client session ended'),
          time: session.ended_at,
        },
      ].filter((event) => event.time > 0)
    : []
  return (
    <Dialog
      open={props.id !== null && allowed}
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={t('Client session details')}
      description={t(
        'Only software presence and model usage metadata are shown. Conversation content is not collected.'
      )}
      contentClassName='min-w-0 sm:max-w-3xl max-sm:w-[calc(100vw-1.5rem)] max-sm:max-w-[calc(100vw-1.5rem)]'
      bodyClassName='space-y-4'
    >
      {query.isPending && <LoadingState />}
      {query.isError && (
        <ErrorState
          title={t('Failed to load client activity')}
          onRetry={() => void query.refetch()}
        />
      )}
      {session && data && (
        <>
          <DetailSection label={t('Session overview')}>
            <DetailRow
              label={t('User')}
              value={
                <span className='break-all'>
                  {session.username} · ID{' '}
                  {formatNumber(session.user_id, locale)}
                </span>
              }
            />
            <DetailRow
              label={t('Client status')}
              value={<ActivityStatus status={session.status} />}
            />
            <DetailRow
              label={t('Platform and version')}
              value={
                <span className='break-all'>
                  {session.platform} · {session.app_version}
                </span>
              }
            />
            <DetailRow
              label={t('Session cumulative online')}
              value={<ActivityDuration seconds={session.online_seconds} />}
            />
            <DetailRow
              label={t('Session cumulative foreground')}
              value={<ActivityDuration seconds={session.foreground_seconds} />}
            />
          </DetailSection>
          <DetailSection label={t('Observed session timeline')}>
            <ol className='space-y-3'>
              {events.map((event) => (
                <li
                  key={event.key}
                  className='flex min-w-0 flex-col gap-1 border-l-2 pl-3 text-sm sm:flex-row sm:justify-between'
                >
                  <span>{event.label}</span>
                  <ActivityTimestamp value={event.time} />
                </li>
              ))}
            </ol>
            <p className='text-muted-foreground mt-3 text-xs'>
              {t(
                'Only observed session start, heartbeat and end times are available; foreground switches are not reconstructed.'
              )}
            </p>
          </DetailSection>
          <DetailSection label={t('Account model usage during session')}>
            <p className='text-muted-foreground mb-3 text-xs'>
              {t(
                'Model usage belongs to this account during the session and may include other devices. Error records may include retries.'
              )}
            </p>
            {data.usage.models.length ? (
              <StaticDataTable
                data={data.usage.models}
                getRowKey={(row) => row.model_name}
                columns={[
                  {
                    id: 'model',
                    header: t('Model'),
                    cell: (row) => (
                      <span className='break-all'>{row.model_name}</span>
                    ),
                  },
                  {
                    id: 'calls',
                    header: t('Calls'),
                    cell: (row) => formatNumber(row.calls, locale),
                  },
                  {
                    id: 'errors',
                    header: t('Error records'),
                    cell: (row) => formatNumber(row.errors, locale),
                  },
                  {
                    id: 'last',
                    header: t('Last model call'),
                    cell: (row) => (
                      <ActivityTimestamp value={row.last_called_at} />
                    ),
                  },
                ]}
              />
            ) : (
              <EmptyState
                title={t('No model calls recorded')}
                className='min-h-28'
              />
            )}
          </DetailSection>
        </>
      )}
    </Dialog>
  )
}
