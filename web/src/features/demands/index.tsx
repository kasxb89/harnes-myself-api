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
import { FilterHorizontalIcon, RefreshIcon } from '@hugeicons/core-free-icons'
import { HugeiconsIcon } from '@hugeicons/react'
import { useMutation, useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { DateTimePicker } from '@/components/datetime-picker'
import {
  sideDrawerContentClassName,
  sideDrawerFormClassName,
  sideDrawerHeaderClassName,
} from '@/components/drawer-layout'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from '@/components/ui/sheet'
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
import { DemandDirectory } from './directory'
import { DemandEngineers } from './engineers'

export function DemandManagementPage() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const [tab, setTab] = useState('demands')
  const [customerFilter, setCustomerFilter] = useState('')
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
        <Tabs
          value={tab}
          onValueChange={setTab}
          className='flex h-full min-h-0 flex-col'
        >
          <TabsList
            aria-label={t('Demand management')}
            variant='line'
            className='mb-3 w-full shrink-0 justify-start overflow-x-auto border-b px-0 group-data-horizontal/tabs:h-10 [&_[data-slot=tabs-trigger]]:flex-none [&_[data-slot=tabs-trigger]]:px-3'
          >
            <TabsTrigger value='demands'>{t('Demands')}</TabsTrigger>
            <TabsTrigger value='service-profiles'>
              {t('Service profiles')}
            </TabsTrigger>
            <TabsTrigger value='customer-profiles'>
              {t('Customer profiles')}
            </TabsTrigger>
            <TabsTrigger value='engineers'>{t('Engineers')}</TabsTrigger>
            <TabsTrigger value='settings'>{t('Service settings')}</TabsTrigger>
          </TabsList>
          <TabsContent value='demands' className='min-h-0 flex-1'>
            <DemandInbox
              key={`admin:${user.id}:${customerFilter}`}
              customerProfileId={customerFilter}
              onClearCustomer={() => setCustomerFilter('')}
              accountId={user.id}
              mode='admin'
              canWrite={canWrite}
            />
          </TabsContent>
          {(['service-profiles', 'customer-profiles'] as const).map((kind) => (
            <TabsContent
              key={kind}
              value={kind}
              className='min-h-0 flex-1 overflow-y-auto'
            >
              <DemandDirectory
                key={`${user.id}:${kind}`}
                accountId={user.id}
                canWrite={canWrite}
                kind={kind}
                onViewDemands={(id) => {
                  setCustomerFilter(id)
                  setTab('demands')
                }}
              />
            </TabsContent>
          ))}
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
  customerProfileId,
  onClearCustomer,
}: {
  customerProfileId?: string
  onClearCustomer?: () => void
  accountId: number
  mode: DemandMode
  canWrite: boolean
}) {
  const { t } = useTranslation()
  const [filters, setFilters] = useState<Record<string, string>>(
    customerProfileId ? { customer_profile_id: customerProfileId } : {}
  )
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
  const hasFilters = Object.entries(filters).some(
    ([name, value]) => name !== 'assignment' && Boolean(value)
  )
  return (
    <div className='grid h-full min-h-0 overflow-hidden rounded-xl border md:grid-cols-[280px_minmax(0,1fr)] lg:grid-cols-[300px_minmax(0,1fr)]'>
      <aside
        className={cn(
          'flex min-h-0 flex-col md:border-r',
          selected && 'hidden md:flex'
        )}
        aria-label={t('Demand list')}
      >
        <div className='flex h-16 shrink-0 items-center gap-1.5 border-b px-3'>
          <NativeSelect
            aria-label={t(mode === 'admin' ? 'Assignment' : 'Status')}
            className='h-9 min-w-0 flex-1 text-sm font-medium [&_select]:h-9 [&_select]:rounded-md [&_select]:border-transparent [&_select]:pl-1 [&_select]:shadow-none'
            value={
              mode === 'admin'
                ? (filters.assignment ?? '')
                : (filters.status ?? '')
            }
            onChange={(e) =>
              updateFilter(
                mode === 'admin' ? 'assignment' : 'status',
                e.target.value
              )
            }
          >
            {mode === 'admin' ? (
              <>
                <option value=''>{t('All demands')}</option>
                <option value='unassigned'>{t('Awaiting assignment')}</option>
                <option value='assigned'>{t('Assigned')}</option>
              </>
            ) : (
              <>
                <option value=''>{t('All statuses')}</option>
                {demandStatuses.map((status) => (
                  <option key={status} value={status}>
                    {t(statusLabels[status])}
                  </option>
                ))}
              </>
            )}
          </NativeSelect>
          <Sheet>
            <SheetTrigger
              render={
                <Button
                  size='icon'
                  variant={hasFilters ? 'secondary' : 'ghost'}
                  aria-label={t('Filter demands')}
                  title={t('Filter demands')}
                />
              }
            >
              <HugeiconsIcon icon={FilterHorizontalIcon} aria-hidden='true' />
            </SheetTrigger>
            <SheetContent
              side='right'
              className={sideDrawerContentClassName('sm:max-w-sm')}
            >
              <SheetHeader className={sideDrawerHeaderClassName()}>
                <SheetTitle>{t('Filter demands')}</SheetTitle>
              </SheetHeader>
              <div className={sideDrawerFormClassName()}>
                <label className='space-y-2 text-sm'>
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
                    <label className='space-y-2 text-sm'>
                      <span className='block'>{t('Engineer')}</span>
                      <NativeSelect
                        value={filters.engineer_id ?? ''}
                        onChange={(e) =>
                          updateFilter('engineer_id', e.target.value)
                        }
                      >
                        <option value=''>{t('All engineers')}</option>
                        {engineers.data?.map((engineer) => (
                          <option key={engineer.id} value={engineer.id}>
                            {engineer.display_name}
                          </option>
                        ))}
                      </NativeSelect>
                    </label>
                    <label className='space-y-2 text-sm'>
                      <span className='block'>{t('User ID')}</span>
                      <Input
                        type='number'
                        min={1}
                        value={filters.user_id ?? ''}
                        onChange={(e) =>
                          updateFilter('user_id', e.target.value)
                        }
                      />
                    </label>
                    <label className='space-y-2 text-sm'>
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
                            date
                              ? String(Math.floor(date.getTime() / 1000))
                              : ''
                          )
                        }
                      />
                    </label>
                    <label className='space-y-2 text-sm'>
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
                            date
                              ? String(Math.floor(date.getTime() / 1000))
                              : ''
                          )
                        }
                      />
                    </label>
                  </>
                )}
                <Button
                  variant='outline'
                  onClick={() => {
                    setFilters(
                      customerProfileId
                        ? { customer_profile_id: customerProfileId }
                        : {}
                    )
                    setOlderCursor('')
                  }}
                >
                  {t('Reset filters')}
                </Button>
              </div>
            </SheetContent>
          </Sheet>
          <Button
            size='icon'
            variant='ghost'
            aria-label={t('Refresh')}
            title={t('Refresh')}
            disabled={query.isFetching}
            onClick={() => {
              void query.refetch()
            }}
          >
            <HugeiconsIcon icon={RefreshIcon} aria-hidden='true' />
          </Button>
        </div>
        {customerProfileId && (
          <Button variant='ghost' size='sm' onClick={onClearCustomer}>
            {t('Clear customer filter')}
          </Button>
        )}
        {mode === 'engineer' && (
          <div className='flex flex-wrap items-center justify-between gap-2 border-b px-3 py-2'>
            <span className='text-muted-foreground text-xs' role='status'>
              {t(presenceLabel)}
            </span>
            {heartbeat.data && (
              <Button
                size='sm'
                variant='ghost'
                disabled={accepting.isPending}
                onClick={() =>
                  accepting.mutate(!heartbeat.data.engineer.accepting)
                }
              >
                {t(
                  heartbeat.data.engineer.accepting
                    ? 'Pause accepting demands'
                    : 'Accepting demands'
                )}
              </Button>
            )}
          </div>
        )}
        <div className='min-h-0 flex-1 overflow-y-auto'>
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
                'hover:bg-muted/60 focus-visible:ring-ring block w-full cursor-pointer space-y-2 border-b px-4 py-4 text-left focus-visible:ring-2 focus-visible:outline-none',
                selected?.id === demand.id &&
                  'bg-primary/5 shadow-[inset_3px_0_0_var(--primary)]'
              )}
            >
              <div className='flex items-center justify-between gap-3'>
                <span className='truncate text-sm font-semibold'>
                  {demand.title}
                </span>
                <time
                  className='text-muted-foreground shrink-0 text-xs'
                  title={formatTimestampToDate(
                    demand.last_message_at ?? demand.updated_at
                  )}
                >
                  {formatTimestampToDate(
                    demand.last_message_at ?? demand.updated_at
                  ).slice(5, 16)}
                </time>
              </div>
              <p className='text-muted-foreground line-clamp-2 text-sm leading-5'>
                {demand.preview}
              </p>
              <div className='flex items-center justify-between gap-2'>
                <span className='text-muted-foreground truncate text-xs'>
                  {demand.owner?.nickname || t('User')} ·{' '}
                  {demand.service_profile_name || demand.engineer.display_name}
                </span>
                {demand.unread_count > 0 ? (
                  <Badge>{demand.unread_count}</Badge>
                ) : (
                  <Badge variant='outline' className='shrink-0 text-[11px]'>
                    {t(
                      demand.assigned === false
                        ? 'Awaiting assignment'
                        : statusLabels[demand.status]
                    )}
                  </Badge>
                )}
              </div>
            </button>
          ))}
          {(olderCursor || query.data?.has_more) && (
            <div className='flex flex-wrap justify-center gap-2 p-3'>
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
                  variant='ghost'
                  onClick={() =>
                    setOlderCursor(query.data?.next_before_id ?? '')
                  }
                >
                  {t('Older demands')}
                </Button>
              )}
            </div>
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
            description={t('Choose a conversation to reply to the customer.')}
          />
        )}
      </div>
    </div>
  )
}
