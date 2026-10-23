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
import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { DataTablePage, useDataTable } from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import { CompactDateTimeRangePicker } from '@/features/usage-logs/components/compact-date-time-range-picker'
import {
  LogsFilterField,
  LogsFilterInput,
  LogsFilterToolbar,
} from '@/features/usage-logs/components/logs-filter-toolbar'
import { canAccessAdminRoute } from '@/lib/admin-permissions'
import dayjs from '@/lib/dayjs'
import { useAuthStore } from '@/stores/auth-store'

import { getActivitySessions, getActivitySummary } from './api'
import { useActivityColumns } from './columns'
import { ActivityDetails } from './details'
import { ActivityTimestamp } from './display'
import { ActivitySummary } from './summary'
import type { ActivityFilters, ClientActivitySession } from './types'

const EMPTY_SESSIONS: ClientActivitySession[] = []
function initialActivityFilters(): ActivityFilters {
  return {
    p: 1,
    page_size: 20,
    start_timestamp: dayjs().startOf('day').unix(),
    end_timestamp: dayjs().endOf('day').unix(),
  }
}
export function UserActivity() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const allowed = canAccessAdminRoute(user, '/user-activity')
  const [filters, setFilters] = useState<ActivityFilters>(
    initialActivityFilters
  )
  const [draftUsername, setDraftUsername] = useState('')
  const [selectedId, setSelectedId] = useState<number | null>(null)
  const invalidRange =
    filters.start_timestamp !== undefined &&
    filters.end_timestamp !== undefined &&
    filters.start_timestamp > filters.end_timestamp
  const sessions = useQuery({
    queryKey: ['client-activity', user?.id, 'sessions', filters],
    queryFn: () => getActivitySessions(filters),
    enabled: allowed && !invalidRange,
    retry: false,
    refetchInterval: 30_000,
  })
  const range = {
    start_timestamp: filters.start_timestamp,
    end_timestamp: filters.end_timestamp,
  }
  const summary = useQuery({
    queryKey: ['client-activity', user?.id, 'summary', range],
    queryFn: () => getActivitySummary(range),
    enabled: allowed && !invalidRange,
    retry: false,
    refetchInterval: 30_000,
  })
  const onDetails = useCallback((id: number) => setSelectedId(id), [])
  const columns = useActivityColumns(onDetails)
  const { table } = useDataTable({
    columns,
    data:
      allowed && !sessions.isError && !invalidRange
        ? (sessions.data?.items ?? EMPTY_SESSIONS)
        : EMPTY_SESSIONS,
    getRowId: (row) => String(row.id),
    totalCount: allowed && !sessions.isError ? (sessions.data?.total ?? 0) : 0,
    pagination: { pageIndex: filters.p - 1, pageSize: filters.page_size },
    onPaginationChange: (updater) =>
      setFilters((previous) => {
        const current = {
          pageIndex: previous.p - 1,
          pageSize: previous.page_size,
        }
        const next = typeof updater === 'function' ? updater(current) : updater
        return {
          ...previous,
          p: next.pageSize === previous.page_size ? next.pageIndex + 1 : 1,
          page_size: next.pageSize,
        }
      }),
    enableRowSelection: false,
    enableSorting: false,
    manualFiltering: true,
    manualPagination: true,
  })
  const update = (patch: Partial<ActivityFilters>) =>
    setFilters((previous) => ({ ...previous, ...patch, p: 1 }))
  const refresh = () => {
    if (!invalidRange) {
      void sessions.refetch()
      void summary.refetch()
    }
  }
  if (!allowed) return <ErrorState title={t('Access denied')} />
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('User Activity')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button
          variant='outline'
          onClick={refresh}
          disabled={invalidRange || sessions.isFetching}
        >
          {t('Refresh')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='min-w-0 space-y-4'>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Software presence is separate from model usage. A session starts with the first report after sign-in; older clients and signed-out usage are not backfilled.'
            )}
          </p>
          <ActivitySummary
            summary={summary.isError || invalidRange ? undefined : summary.data}
            loading={summary.isPending && !invalidRange}
            error={summary.isError || invalidRange}
          />
          <div className='text-muted-foreground space-y-1 text-xs'>
            <p>
              {t(
                'Online counts are current and global. Session starts and model usage use the selected period; durations are cumulative for sessions started in that period.'
              )}
            </p>
            <p>
              {t(
                'Clients without reporting, including older versions, cannot be distinguished from unused software. Offline means heartbeat timed out or the session ended.'
              )}
            </p>
            {summary.data && (
              <p>
                {t('Server observation time')}:{' '}
                <ActivityTimestamp value={summary.data.server_time} /> ·{' '}
                {t('Heartbeat timeout seconds', {
                  seconds: summary.data.online_timeout_seconds,
                })}
              </p>
            )}
          </div>
          <DataTablePage
            table={table}
            columns={columns}
            isLoading={sessions.isPending && !invalidRange}
            isFetching={sessions.isFetching}
            emptyTitle={
              sessions.isError
                ? t('Failed to load client activity')
                : t('No client sessions')
            }
            emptyDescription={t(
              'Only reporting clients appear here. Try another user or period.'
            )}
            paginationInFooter={false}
            className='h-auto'
            applyHeaderSize
            pinnedColumns={[{ columnId: 'actions', side: 'right' }]}
            toolbar={
              <div className='space-y-2'>
                <LogsFilterToolbar
                  table={table}
                  primaryFilters={
                    <>
                      <LogsFilterField>
                        <LogsFilterInput
                          aria-label={t('Username')}
                          placeholder={t('Username')}
                          value={draftUsername}
                          onChange={(event) =>
                            setDraftUsername(event.target.value)
                          }
                          onKeyDown={(event) => {
                            if (event.key === 'Enter') {
                              update({
                                username: draftUsername.trim() || undefined,
                              })
                            }
                          }}
                        />
                      </LogsFilterField>
                      <LogsFilterField>
                        <Combobox
                          aria-label={t('Client status')}
                          value={filters.status ?? 'all'}
                          options={[
                            { value: 'all', label: t('All statuses') },
                            { value: 'online', label: t('Online') },
                            {
                              value: 'foreground',
                              label: t('Active foreground'),
                            },
                            {
                              value: 'background',
                              label: t('Background or idle'),
                            },
                            { value: 'offline', label: t('Offline') },
                          ]}
                          onValueChange={(value) => {
                            if (value) {
                              update({
                                status: value as ActivityFilters['status'],
                              })
                            }
                          }}
                          className='w-full'
                        />
                      </LogsFilterField>
                      <LogsFilterField wide>
                        <CompactDateTimeRangePicker
                          start={
                            filters.start_timestamp === undefined
                              ? undefined
                              : new Date(filters.start_timestamp * 1000)
                          }
                          end={
                            filters.end_timestamp === undefined
                              ? undefined
                              : new Date(filters.end_timestamp * 1000)
                          }
                          onChange={({ start, end }) =>
                            update({
                              start_timestamp: start
                                ? Math.floor(start.getTime() / 1000)
                                : undefined,
                              end_timestamp: end
                                ? Math.floor(end.getTime() / 1000)
                                : undefined,
                            })
                          }
                        />
                      </LogsFilterField>
                    </>
                  }
                  hasActiveFilters
                  onSearch={() =>
                    update({ username: draftUsername.trim() || undefined })
                  }
                  onReset={() => {
                    setDraftUsername('')
                    setFilters(initialActivityFilters())
                  }}
                  searchLoading={sessions.isFetching}
                />
                {invalidRange && (
                  <Alert variant='destructive'>
                    <AlertDescription>
                      {t('End time must be after start time')}
                    </AlertDescription>
                  </Alert>
                )}
                {(sessions.isError || summary.isError) && (
                  <Alert variant='destructive'>
                    <AlertDescription className='flex items-center justify-between gap-2'>
                      <span>{t('Failed to load client activity')}</span>
                      <Button size='sm' variant='outline' onClick={refresh}>
                        {t('Retry')}
                      </Button>
                    </AlertDescription>
                  </Alert>
                )}
              </div>
            }
          />
        </div>
        <ActivityDetails id={selectedId} onClose={() => setSelectedId(null)} />
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
