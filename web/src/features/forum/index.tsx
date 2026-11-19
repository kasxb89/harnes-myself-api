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
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { StaticDataTable } from '@/components/data-table'
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
} from '@/components/ui/sheet'
import { Switch } from '@/components/ui/switch'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { canAccessAdminRoute, hasPermission } from '@/lib/admin-permissions'
import { formatTimestampToDate } from '@/lib/format'
import { useAuthStore } from '@/stores/auth-store'

import {
  loadConfig,
  loadContent,
  moderate,
  saveConfig,
  type ContentKind,
  type ForumContent,
} from './api'

export function ForumManagementPage() {
  const { t } = useTranslation()
  const user = useAuthStore((s) => s.auth.user)
  const [tab, setTab] = useState('posts')
  if (!user || !canAccessAdminRoute(user, '/forum-management')) {
    return <ErrorState title={t('Access denied')} />
  }
  const writable = hasPermission(user, 'forum', 'write')
  return (
    <SectionPageLayout fixedContent>
      <SectionPageLayout.Title>{t('Forum management')}</SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <Tabs
          value={tab}
          onValueChange={setTab}
          className='flex h-full min-h-0 flex-col'
        >
          <TabsList
            variant='line'
            className='mb-4 w-full shrink-0 justify-start border-b'
          >
            <TabsTrigger value='posts'>{t('Forum posts')}</TabsTrigger>
            <TabsTrigger value='comments'>{t('Forum comments')}</TabsTrigger>
            <TabsTrigger value='settings'>{t('Forum settings')}</TabsTrigger>
          </TabsList>
          {(['posts', 'comments'] as const).map((kind) => (
            <TabsContent
              value={kind}
              key={kind}
              className='min-h-0 flex-1 overflow-y-auto'
            >
              <ContentList
                key={`${user.id}:${kind}`}
                kind={kind}
                accountId={user.id}
                writable={writable}
              />
            </TabsContent>
          ))}
          <TabsContent value='settings'>
            <ForumSettings
              key={user.id}
              accountId={user.id}
              writable={writable}
            />
          </TabsContent>
        </Tabs>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

function ContentList({
  kind,
  accountId,
  writable,
}: {
  kind: ContentKind
  accountId: number
  writable: boolean
}) {
  const { t } = useTranslation()
  const cache = useQueryClient()
  const [review, setReview] = useState('unreviewed')
  const [input, setInput] = useState('')
  const [q, setQ] = useState('')
  const [cursors, setCursors] = useState([''])
  const [selected, setSelected] = useState<ForumContent | null>(null)
  const [removing, setRemoving] = useState(false)
  const [reason, setReason] = useState('')
  const data = useQuery({
    queryKey: ['forum', accountId, kind, review, q, cursors.at(-1)],
    queryFn: ({ signal }) =>
      loadContent(kind, review, q, cursors.at(-1) ?? '', signal),
  })
  const action = useMutation({
    mutationFn: ({ row, op }: { row: ForumContent; op: 'review' | 'delete' }) =>
      moderate(kind, row.id, op, reason),
    onSuccess: () => {
      setSelected(null)
      setRemoving(false)
      setReason('')
      void cache.invalidateQueries({ queryKey: ['forum', accountId] })
      toast.success(t('Saved successfully'))
    },
    onError: () => {
      toast.error(t('Operation failed. Please try again.'))
    },
  })
  const status = (row: ForumContent) => {
    if (row.deleted_at) return t('Removed')
    if (row.reviewed_at) return t('Reviewed')
    return t('Unreviewed')
  }
  return (
    <div className='space-y-4'>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Posts and comments appear immediately. Review them here after publication.'
        )}
      </p>
      <form
        className='flex flex-wrap items-center gap-2'
        onSubmit={(e) => {
          e.preventDefault()
          setQ(input)
          setCursors([''])
        }}
      >
        <NativeSelect
          aria-label={t('Review status')}
          value={review}
          onChange={(e) => {
            setReview(e.target.value)
            setCursors([''])
          }}
        >
          <option value='unreviewed'>{t('Unreviewed')}</option>
          <option value='reviewed'>{t('Reviewed')}</option>
          <option value='deleted'>{t('Removed')}</option>
          <option value='all'>{t('All')}</option>
        </NativeSelect>
        <Input
          className='w-full sm:w-64'
          value={input}
          onChange={(e) => setInput(e.target.value)}
          maxLength={100}
          placeholder={t('Search content')}
          aria-label={t('Search content')}
        />
        <Button type='submit' variant='outline'>
          {t('Search')}
        </Button>
        <Button
          type='button'
          variant='ghost'
          onClick={() => {
            void data.refetch()
          }}
        >
          {t('Refresh')}
        </Button>
      </form>
      {data.isPending ? <LoadingState /> : null}
      {data.isError ? (
        <ErrorState
          title={t('Failed to load')}
          onRetry={() => {
            void data.refetch()
          }}
        />
      ) : null}
      {data.data && !data.isError ? (
        <StaticDataTable
          data={data.data.items}
          getRowKey={(row) => row.id}
          emptyContent={t('No forum content')}
          columns={[
            {
              id: 'body',
              header: t('Content'),
              cell: (row) => (
                <button
                  type='button'
                  className='max-w-lg text-left hover:underline'
                  onClick={() => {
                    setSelected(row)
                    setReason('')
                  }}
                >
                  <span className='line-clamp-2 break-all'>
                    {row.title ?? row.body}
                  </span>
                  {row.post_title ? (
                    <span className='text-muted-foreground mt-1 block text-xs'>
                      {row.post_title}
                    </span>
                  ) : null}
                </button>
              ),
            },
            {
              id: 'author',
              header: t('Author'),
              cell: (row) => (
                <span>
                  {row.author.display_name || t('Forum member')}
                  <span className='text-muted-foreground block text-xs'>
                    ID {row.author.id}
                  </span>
                </span>
              ),
            },
            {
              id: 'time',
              header: t('Published at'),
              cell: (row) => formatTimestampToDate(row.created_at),
            },
            {
              id: 'status',
              header: t('Review status'),
              cell: (row) => <Badge variant='secondary'>{status(row)}</Badge>,
            },
            {
              id: 'actions',
              header: t('Actions'),
              cell: (row) => (
                <Button
                  variant='ghost'
                  size='sm'
                  onClick={() => {
                    setSelected(row)
                    setReason('')
                  }}
                >
                  {t('View')}
                </Button>
              ),
            },
          ]}
        />
      ) : null}
      <div className='flex justify-end gap-2'>
        <Button
          variant='outline'
          disabled={cursors.length === 1}
          onClick={() => setCursors((c) => c.slice(0, -1))}
        >
          {t('Previous')}
        </Button>
        <Button
          variant='outline'
          disabled={!data.data?.has_more}
          onClick={() => {
            if (data.data?.next_cursor) {
              setCursors((c) => [...c, data.data.next_cursor])
            }
          }}
        >
          {t('Next')}
        </Button>
      </div>
      <Sheet
        open={selected !== null}
        onOpenChange={(open) => {
          if (!open) setSelected(null)
        }}
      >
        <SheetContent className='flex w-full flex-col sm:max-w-2xl'>
          <SheetHeader>
            <SheetTitle>
              {selected?.title ?? selected?.post_title ?? t('Forum comment')}
            </SheetTitle>
          </SheetHeader>
          {selected ? (
            <div className='flex min-h-0 flex-1 flex-col gap-5 overflow-y-auto p-5'>
              <div className='text-muted-foreground text-sm'>
                {selected.author.display_name || t('Forum member')} ·{' '}
                {formatTimestampToDate(selected.created_at)} ·{' '}
                {status(selected)}
              </div>
              <div className='text-sm leading-7 break-words whitespace-pre-wrap'>
                {selected.body}
              </div>
              {selected.deleted_at ? (
                <p className='text-muted-foreground text-sm'>
                  {t('Removal reason')}:{' '}
                  {selected.delete_reason || t('Not specified')}
                </p>
              ) : null}
              {!selected.deleted_at && writable ? (
                <div className='mt-auto flex gap-2 border-t pt-4'>
                  <Button
                    disabled={action.isPending || !!selected.reviewed_at}
                    onClick={() =>
                      action.mutate({ row: selected, op: 'review' })
                    }
                  >
                    {t('Mark reviewed')}
                  </Button>
                  <Button
                    variant='destructive'
                    disabled={action.isPending}
                    onClick={() => setRemoving(true)}
                  >
                    {t('Remove content')}
                  </Button>
                </div>
              ) : null}
            </div>
          ) : null}
        </SheetContent>
      </Sheet>
      <ConfirmDialog
        open={removing}
        onOpenChange={setRemoving}
        title={t('Remove content')}
        desc={t(
          'This content will disappear for users. The moderation record will be retained.'
        )}
        destructive
        confirmText={t('Remove')}
        isLoading={action.isPending}
        handleConfirm={() => {
          if (selected) action.mutate({ row: selected, op: 'delete' })
        }}
      >
        <Input
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          maxLength={1000}
          placeholder={t('Removal reason (optional)')}
          aria-label={t('Removal reason (optional)')}
        />
      </ConfirmDialog>
    </div>
  )
}

function ForumSettings({
  accountId,
  writable,
}: {
  accountId: number
  writable: boolean
}) {
  const { t } = useTranslation()
  const cache = useQueryClient()
  const config = useQuery({
    queryKey: ['forum', accountId, 'config'],
    queryFn: ({ signal }) => loadConfig(signal),
  })
  const save = useMutation({
    mutationFn: saveConfig,
    onSuccess: () => {
      void cache.invalidateQueries({ queryKey: ['forum', accountId, 'config'] })
      toast.success(t('Saved successfully'))
    },
    onError: () => {
      toast.error(
        t('Settings changed or could not be saved. Refresh and try again.')
      )
      void config.refetch()
    },
  })
  if (config.isPending) return <LoadingState />
  if (config.isError) {
    return (
      <ErrorState
        title={t('Failed to load')}
        onRetry={() => {
          void config.refetch()
        }}
      />
    )
  }
  return (
    <div className='flex max-w-2xl items-center justify-between gap-6 border-b py-5'>
      <div>
        <label htmlFor='forum-enabled' className='font-medium'>
          {t('Enable forum')}
        </label>
        <p className='text-muted-foreground mt-1 text-sm'>
          {t(
            'Turning this off stops new posts, comments and likes. Existing discussions remain readable.'
          )}
        </p>
      </div>
      <Switch
        id='forum-enabled'
        disabled={!writable || save.isPending}
        checked={config.data.enabled}
        onCheckedChange={(enabled) =>
          save.mutate({ revision: config.data.revision, enabled })
        }
      />
    </div>
  )
}
