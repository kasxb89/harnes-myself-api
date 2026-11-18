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
import { useMutation, useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { DateTimePicker } from '@/components/datetime-picker'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { canAccessAdminRoute, hasPermission } from '@/lib/admin-permissions'
import { formatTimestampToDate } from '@/lib/format'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import {
  demandStatuses,
  heartbeatEngineer,
  loadDemands,
  loadEngineers,
  statusLabels,
  type Demand,
  type DemandMode,
} from './api'
import { DemandSettings } from './config-editor'
import { DemandConversation } from './conversation'
import { DemandEngineers } from './engineers'

export function DemandManagementPage() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  if (!user || !canAccessAdminRoute(user, '/demand-management')) {
    return <ErrorState title={t('Access denied')} />
  }
  const canWrite = hasPermission(user, 'demands', 'write')
  return (
    <SectionPageLayout fixedContent>
      <SectionPageLayout.Title>
        {t('Demand management')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <Tabs defaultValue='demands' className='flex h-full min-h-0 flex-col'>
          <TabsList
            aria-label={t('Demand management')}
            className='mb-4 shrink-0'
          >
            <TabsTrigger value='demands'>{t('Demands')}</TabsTrigger>
            <TabsTrigger value='engineers'>{t('Engineers')}</TabsTrigger>
            <TabsTrigger value='settings'>{t('Service settings')}</TabsTrigger>
          </TabsList>
          <TabsContent value='demands' className='min-h-0 flex-1'>
            <DemandInbox
              key={`admin:${user.id}`}
              accountId={user.id}
              mode='admin'
              canWrite={canWrite}
            />
          </TabsContent>
          <TabsContent
            value='engineers'
            className='min-h-0 flex-1 overflow-y-auto'
          >
            <DemandEngineers
              key={user.id}
              accountId={user.id}
              canWrite={canWrite}
            />
          </TabsContent>
          <TabsContent
            value='settings'
            className='min-h-0 flex-1 overflow-y-auto'
          >
            <DemandSettings
              key={user.id}
              accountId={user.id}
              canWrite={canWrite}
            />
          </TabsContent>
        </Tabs>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
export function EngineerWorkspacePage() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  if (!user) return <ErrorState title={t('Sign in required')} />
  return (
    <SectionPageLayout fixedContent>
      <SectionPageLayout.Title>
        {t('Engineer workspace')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <DemandInbox
          key={`engineer:${user.id}`}
          accountId={user.id}
          mode='engineer'
          canWrite
        />
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
function DemandInbox({
  accountId,
  mode,
  canWrite,
}: {
  accountId: number
  mode: DemandMode
  canWrite: boolean
}) {
  const { t } = useTranslation()
  const [filters, setFilters] = useState<Record<string, string>>({})
  const [selected, setSelected] = useState<Demand | null>(null)
  const [olderCursor, setOlderCursor] = useState('')
  const query = useQuery({
    queryKey: ['demands', accountId, mode, filters, olderCursor],
    queryFn: ({ signal }) =>
      loadDemands(
        mode,
        { ...filters, ...(olderCursor ? { before_id: olderCursor } : {}) },
        signal
      ),
    retry: false,
    refetchInterval: 15000,
  })
  const engineers = useQuery({
    queryKey: ['demand-engineers', accountId],
    enabled: mode === 'admin',
    queryFn: ({ signal }) => loadEngineers(signal),
    retry: false,
  })
  const heartbeat = useQuery({
    queryKey: ['demand-heartbeat', accountId],
    enabled: mode === 'engineer',
    queryFn: () => heartbeatEngineer(),
    refetchInterval: 30000,
    refetchIntervalInBackground: false,
    retry: false,
  })
  const accepting = useMutation({
    mutationFn: (value: boolean) => heartbeatEngineer(value),
    retry: false,
    onSuccess: () => {
      void heartbeat.refetch()
    },
  })
  const updateFilter = (name: string, value: string) => {
    setFilters((old) => ({ ...old, [name]: value }))
    setOlderCursor('')
  }
  let presenceLabel = 'Connecting...'
  if (heartbeat.data?.presence === 'online') {
    presenceLabel = 'Online, accepting demands'
  }
  if (heartbeat.data?.presence === 'busy') presenceLabel = 'Busy'
  if (heartbeat.isError) presenceLabel = 'Engineer access unavailable'
  const rows = query.data?.items ?? []
  return (
    <div className='flex h-full min-h-0 flex-col gap-4'>
      <div className='flex shrink-0 flex-wrap items-end gap-3'>
        <label className='space-y-1 text-sm'>
          <span className='block'>{t('Status')}</span>
          <NativeSelect
            value={filters.status ?? ''}
            onChange={(e) => updateFilter('status', e.target.value)}
          >
            <option value=''>{t('All statuses')}</option>
            {demandStatuses.map((status) => (
              <option key={status} value={status}>
                {t(statusLabels[status])}
              </option>
            ))}
          </NativeSelect>
        </label>
        {mode === 'admin' && (
          <>
            <label className='space-y-1 text-sm'>
              <span className='block'>{t('Engineer')}</span>
              <NativeSelect
                value={filters.engineer_id ?? ''}
                onChange={(e) => updateFilter('engineer_id', e.target.value)}
              >
                <option value=''>{t('All engineers')}</option>
                {engineers.data?.map((engineer) => (
                  <option value={engineer.id} key={engineer.id}>
                    {engineer.display_name}
                  </option>
                ))}
              </NativeSelect>
            </label>
            <label className='space-y-1 text-sm'>
              <span className='block'>{t('User ID')}</span>
              <Input
                type='number'
                min={1}
                value={filters.user_id ?? ''}
                onChange={(e) => updateFilter('user_id', e.target.value)}
                className='w-32'
              />
            </label>
          </>
        )}
        {mode === 'admin' && (
          <>
            <label className='space-y-1 text-sm'>
              <span className='block'>{t('Created after')}</span>
              <DateTimePicker
                value={
                  filters.created_after
                    ? new Date(Number(filters.created_after) * 1000)
                    : undefined
                }
                onChange={(date) =>
                  updateFilter(
                    'created_after',
                    date ? String(Math.floor(date.getTime() / 1000)) : ''
                  )
                }
              />
            </label>
            <label className='space-y-1 text-sm'>
              <span className='block'>{t('Created before')}</span>
              <DateTimePicker
                value={
                  filters.created_before
                    ? new Date(Number(filters.created_before) * 1000)
                    : undefined
                }
                onChange={(date) =>
                  updateFilter(
                    'created_before',
                    date ? String(Math.floor(date.getTime() / 1000)) : ''
                  )
                }
              />
            </label>
          </>
        )}
        <Button
          variant='outline'
          disabled={query.isFetching}
          onClick={() => {
            void query.refetch()
          }}
        >
          {t('Refresh')}
        </Button>
        {mode === 'engineer' && heartbeat.data && (
          <Button
            variant='outline'
            disabled={accepting.isPending}
            onClick={() => accepting.mutate(!heartbeat.data.engineer.accepting)}
          >
            {t(
              heartbeat.data.engineer.accepting
                ? 'Pause accepting demands'
                : 'Accepting demands'
            )}
          </Button>
        )}
        {mode === 'engineer' && (
          <span className='text-muted-foreground text-sm' role='status'>
            {t(presenceLabel)}
          </span>
        )}
      </div>
      <div className='grid min-h-0 flex-1 overflow-hidden rounded-xl border md:grid-cols-[minmax(260px,340px)_minmax(0,1fr)]'>
        <aside
          className={cn(
            'min-h-0 overflow-y-auto md:border-r',
            selected && 'hidden md:block'
          )}
          aria-label={t('Demand list')}
        >
          {query.isPending && <LoadingState />}
          {query.isError && (
            <ErrorState
              description={t('Demands could not be refreshed')}
              onRetry={() => {
                void query.refetch()
              }}
            />
          )}
          {query.data && rows.length === 0 && (
            <EmptyState
              title={t('No demands yet')}
              description={t(
                mode === 'engineer'
                  ? 'Only demands assigned to you appear here.'
                  : 'Customer demands appear here after their first message is saved.'
              )}
            />
          )}
          {rows.map((demand) => (
            <button
              type='button'
              key={demand.id}
              onClick={() => setSelected(demand)}
              aria-current={selected?.id === demand.id ? 'true' : undefined}
              className={cn(
                'hover:bg-muted focus-visible:ring-ring block w-full cursor-pointer space-y-2 border-b p-4 text-left focus-visible:ring-2 focus-visible:outline-none',
                selected?.id === demand.id && 'bg-muted'
              )}
            >
              <div className='flex items-start justify-between gap-2'>
                <span className='line-clamp-2 text-sm font-medium break-words'>
                  {demand.title}
                </span>
                {demand.unread_count > 0 && (
                  <Badge>{demand.unread_count}</Badge>
                )}
              </div>
              <p className='text-muted-foreground truncate text-xs'>
                {demand.engineer.display_name} ·{' '}
                {t(statusLabels[demand.status])}
              </p>
              <p className='text-muted-foreground line-clamp-2 text-sm'>
                {demand.preview}
              </p>
              <p className='text-muted-foreground text-xs break-all'>
                {demand.number}
              </p>
              <p className='text-muted-foreground text-xs'>
                {t('User ID')}: {demand.user_id} · {t('Created')}:{' '}
                {formatTimestampToDate(demand.created_at)}
              </p>
              <time className='text-muted-foreground text-xs'>
                {t('Last reply')}:{' '}
                {formatTimestampToDate(
                  demand.last_message_at ?? demand.updated_at
                )}
              </time>
            </button>
          ))}
          <div className='flex flex-wrap gap-2 p-3'>
            {olderCursor && (
              <Button
                size='sm'
                variant='ghost'
                onClick={() => setOlderCursor('')}
              >
                {t('Newest demands')}
              </Button>
            )}
            {query.data?.has_more && (
              <Button
                size='sm'
                variant='outline'
                onClick={() => setOlderCursor(query.data?.next_before_id ?? '')}
              >
                {t('Older demands')}
              </Button>
            )}
          </div>
        </aside>
        <div className={cn('min-h-0 min-w-0', !selected && 'hidden md:block')}>
          {selected ? (
            <DemandConversation
              key={`${accountId}:${selected.id}`}
              initial={rows.find((row) => row.id === selected.id) ?? selected}
              accountId={accountId}
              mode={mode}
              canWrite={canWrite}
              engineers={engineers.data ?? []}
              onBack={() => setSelected(null)}
            />
          ) : (
            <EmptyState
              title={t('Select a demand')}
              description={t(
                'Messages, private attachments and delivery history stay with the original demand.'
              )}
            />
          )}
        </div>
      </div>
    </div>
  )
}
