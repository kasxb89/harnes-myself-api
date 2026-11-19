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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { formatTimestampToDate } from '@/lib/format'
import { cn } from '@/lib/utils'

import {
  communityStatuses,
  communityStatusLabels,
  loadCommunityConfig,
  loadCommunityOrders,
  saveCommunityConfig,
  type CommunityOrder,
} from './community-api'
import { CommunityConversation } from './community-conversation'

export function CommunityInbox(props: {
  accountId: number
  canWrite: boolean
}) {
  const { t } = useTranslation()
  const cache = useQueryClient()
  const [filters, setFilters] = useState({
    status: '',
    visibility: 'all',
    publisher_id: '',
    acceptor_id: '',
    q: '',
    cursor: '',
  })
  const [history, setHistory] = useState<string[]>([])
  const [selected, setSelected] = useState<CommunityOrder | null>(null)
  const [mobileDetail, setMobileDetail] = useState(false)
  const configKey = ['community-config', props.accountId]
  const config = useQuery({
    queryKey: configKey,
    queryFn: ({ signal }) => loadCommunityConfig(signal),
    retry: false,
  })
  const save = useMutation({
    mutationFn: saveCommunityConfig,
    retry: false,
    onSuccess: () => {
      void cache.invalidateQueries({ queryKey: configKey })
    },
  })
  const list = useQuery({
    queryKey: ['community-orders', props.accountId, filters],
    queryFn: ({ signal }) => loadCommunityOrders(filters, signal),
    retry: false,
    refetchInterval: 5000,
  })
  const filter = (key: keyof typeof filters, value: string) => {
    setFilters((v) => ({ ...v, [key]: value, cursor: '' }))
    setHistory([])
  }
  return (
    <div className='flex h-full min-h-0 flex-col gap-3'>
      <div className='flex shrink-0 flex-wrap items-center gap-2'>
        <Input
          className='w-52'
          aria-label={t('Search community orders')}
          placeholder={t('Search community orders')}
          value={filters.q}
          onChange={(e) => filter('q', e.target.value)}
          maxLength={100}
        />
        <NativeSelect
          className='w-44'
          aria-label={t('Status')}
          value={filters.status}
          onChange={(e) => filter('status', e.target.value)}
        >
          <option value=''>{t('All statuses')}</option>
          {communityStatuses.map((s) => (
            <option key={s} value={s}>
              {t(communityStatusLabels[s])}
            </option>
          ))}
        </NativeSelect>
        <NativeSelect
          className='w-36'
          aria-label={t('Listing visibility')}
          value={filters.visibility}
          onChange={(e) => filter('visibility', e.target.value)}
        >
          <option value='all'>{t('All listings')}</option>
          <option value='published'>{t('Visible listings')}</option>
          <option value='hidden'>{t('Hidden listings')}</option>
        </NativeSelect>
        <Button variant='ghost' size='sm' onClick={() => void list.refetch()}>
          {t('Refresh')}
        </Button>
        {config.data && (
          <div className='ml-auto flex items-center gap-2 text-xs'>
            <span className='text-muted-foreground'>
              {t(
                config.data.enabled
                  ? 'Community entry enabled'
                  : 'Community entry disabled'
              )}
            </span>
            {props.canWrite && (
              <Button
                size='sm'
                variant='outline'
                disabled={save.isPending}
                onClick={() =>
                  config.data &&
                  save.mutate({
                    revision: config.data.revision,
                    enabled: !config.data.enabled,
                  })
                }
              >
                {t(config.data.enabled ? 'Disable entry' : 'Enable entry')}
              </Button>
            )}
          </div>
        )}
      </div>
      <div className='grid min-h-0 flex-1 overflow-hidden rounded-lg border md:grid-cols-[280px_minmax(0,1fr)] xl:grid-cols-[320px_minmax(0,1fr)]'>
        <aside
          aria-label={t('Community order list')}
          className={cn(
            'min-h-0 flex-col border-r',
            mobileDetail ? 'hidden md:flex' : 'flex'
          )}
        >
          <div className='min-h-0 flex-1 overflow-y-auto'>
            {list.isPending && <LoadingState />}
            {list.isError && <ErrorState onRetry={() => void list.refetch()} />}
            {list.data?.items.length === 0 && (
              <EmptyState title={t('No community orders yet')} />
            )}
            {list.data?.items.map((order) => (
              <Button
                key={order.id}
                type='button'
                variant='ghost'
                aria-current={selected?.id === order.id ? 'true' : undefined}
                className={cn(
                  'hover:bg-muted/60 block h-auto w-full rounded-none border-b px-4 py-3.5 text-left whitespace-normal',
                  selected?.id === order.id && 'bg-muted/60'
                )}
                onClick={() => {
                  setSelected(order)
                  setMobileDetail(true)
                }}
              >
                <div className='flex items-start gap-2'>
                  <span className='line-clamp-2 min-w-0 flex-1 text-sm font-medium'>
                    {order.title}
                  </span>
                  {order.unread_count > 0 && (
                    <Badge>{order.unread_count}</Badge>
                  )}
                </div>
                <p className='text-muted-foreground mt-1.5 truncate text-xs'>
                  {order.publisher.display_name || t('Publisher')} →{' '}
                  {order.acceptor?.display_name || t('Not accepted')}
                </p>
                <div className='text-muted-foreground mt-2 flex items-center gap-2 text-[11px]'>
                  <span>{t(communityStatusLabels[order.status])}</span>
                  {!order.listing_visible && <span>{t('Hidden')}</span>}
                  <time className='ml-auto'>
                    {formatTimestampToDate(order.created_at).slice(5, 16)}
                  </time>
                </div>
              </Button>
            ))}
          </div>
          <div className='flex shrink-0 justify-between border-t p-2'>
            <Button
              size='sm'
              variant='ghost'
              disabled={history.length === 0}
              onClick={() => {
                const previous = history.at(-1) ?? ''
                setHistory((v) => v.slice(0, -1))
                setFilters((v) => ({ ...v, cursor: previous }))
              }}
            >
              {t('Previous')}
            </Button>
            <Button
              size='sm'
              variant='ghost'
              disabled={!list.data?.has_more}
              onClick={() => {
                setHistory((v) => [...v, filters.cursor])
                setFilters((v) => ({
                  ...v,
                  cursor: list.data?.next_cursor ?? '',
                }))
              }}
            >
              {t('Next')}
            </Button>
          </div>
        </aside>
        <div
          className={cn('min-h-0 min-w-0', !mobileDetail && 'hidden md:block')}
        >
          {selected ? (
            <CommunityConversation
              key={`${props.accountId}:${selected.id}`}
              initial={selected}
              accountId={props.accountId}
              canWrite={props.canWrite}
              onBack={() => setMobileDetail(false)}
            />
          ) : (
            <EmptyState
              title={t('Select an order to view all communication')}
            />
          )}
        </div>
      </div>
    </div>
  )
}
