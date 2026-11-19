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
import { useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'
import { formatTimestampToDate } from '@/lib/format'

import {
  attachmentLink,
  demandAction,
  loadConversation,
  loadDemandAudit,
  loadUploadRules,
  readDemand,
  sendDemandMessage,
  statusLabels,
  uploadDemandFile,
  type Attachment,
  type Demand,
  type DemandMode,
  type Engineer,
  type Message,
} from './api'

type MessageOutbox = {
  client_message_id: string
  text: string
  attachment_ids: string[]
}
interface AttachmentDraft {
  key: string
  file: File
  progress: number
  attachment?: Attachment
  failed: boolean
}
type ConversationData = Awaited<ReturnType<typeof loadConversation>>
function mergeMessages(a: Message[], b: Message[]) {
  return [...new Map([...a, ...b].map((m) => [m.id, m])).values()].sort(
    (x, y) => x.seq - y.seq
  )
}
function loadOutbox(key: string): MessageOutbox | null {
  try {
    const raw = sessionStorage.getItem(key)
    if (!raw) return null
    const value: unknown = JSON.parse(raw)
    if (
      typeof value !== 'object' ||
      !value ||
      !('client_message_id' in value) ||
      !('text' in value) ||
      !('attachment_ids' in value) ||
      typeof value.client_message_id !== 'string' ||
      typeof value.text !== 'string' ||
      !Array.isArray(value.attachment_ids) ||
      !value.attachment_ids.every((id) => typeof id === 'string')
    ) {
      return null
    }
    return {
      client_message_id: value.client_message_id,
      text: value.text,
      attachment_ids: value.attachment_ids,
    }
  } catch {
    return null
  }
}

export function DemandConversation({
  initial,
  accountId,
  mode,
  canWrite,
  engineers,
  onBack,
}: {
  initial: Demand
  accountId: number
  mode: DemandMode
  canWrite: boolean
  engineers: Engineer[]
  onBack: () => void
}) {
  const { t } = useTranslation()
  const cache = useQueryClient()
  const key = ['demand-conversation', accountId, mode, initial.id] as const
  const outboxKey =
    mode === 'admin'
      ? `demand-message:${accountId}:admin:${initial.id}`
      : `demand-message:${accountId}:${initial.id}`
  const [outbox, setOutbox] = useState(() => loadOutbox(outboxKey))
  const [text, setText] = useState(outbox?.text ?? '')
  const [files, setFiles] = useState<AttachmentDraft[]>([])
  const [note, setNote] = useState('')
  const [composer, setComposer] = useState<'reply' | 'note'>('reply')
  const [assignment, setAssignment] = useState('')
  const [closeOpen, setCloseOpen] = useState(false)
  const [localError, setLocalError] = useState('')
  const uploads = useRef(new Map<string, AbortController>())
  useEffect(() => {
    const active = uploads.current
    return () => {
      for (const controller of active.values()) controller.abort()
    }
  }, [])
  const rules = useQuery({
    queryKey: ['demand-upload-rules', accountId, mode],
    enabled: canWrite,
    queryFn: ({ signal }) => loadUploadRules(signal, mode),
    retry: false,
  })
  const actionRef = useRef<{
    signature: string
    body: Record<string, unknown>
  } | null>(null)
  const conversation = useQuery({
    queryKey: key,
    retry: false,
    refetchInterval: 3000,
    refetchIntervalInBackground: false,
    queryFn: async ({ signal }) => {
      const previous = cache.getQueryData<ConversationData>(key)
      let after =
        previous?.items.at(-1)?.seq ?? Math.max(0, initial.last_seq - 100)
      let response = await loadConversation(mode, initial.id, after, signal)
      let items = mergeMessages(previous?.items ?? [], response.items)
      for (let page = 0; response.has_more && page < 4; page++) {
        after = response.next_after_seq
        response = await loadConversation(mode, initial.id, after, signal)
        items = mergeMessages(items, response.items)
      }
      const peerRead = response.peer_read_seq
      if (peerRead !== undefined) {
        items = items.map((message) =>
          message.sender_id === accountId
            ? { ...message, read: message.seq <= peerRead }
            : message
        )
      }
      return { ...response, items }
    },
  })
  const audit = useQuery({
    queryKey: ['demand-audit', accountId, initial.id],
    enabled: mode === 'admin',
    queryFn: async ({ signal }) => {
      const prior = cache.getQueryData<
        Awaited<ReturnType<typeof loadDemandAudit>>
      >(['demand-audit', accountId, initial.id])
      const page = await loadDemandAudit(
        initial.id,
        signal,
        prior?.next_after_id ?? 0
      )
      return {
        ...page,
        items: [
          ...new Map(
            [...(prior?.items ?? []), ...page.items].map((event) => [
              event.id,
              event,
            ])
          ).values(),
        ],
      }
    },
    retry: false,
    refetchInterval: 15000,
  })
  const invalidate = () => {
    void cache.invalidateQueries({ queryKey: ['demands', accountId] })
    void cache.invalidateQueries({ queryKey: key })
    void cache.invalidateQueries({
      queryKey: ['demand-audit', accountId, initial.id],
    })
  }
  const action = useMutation({
    mutationFn: (body: Record<string, unknown>) =>
      demandAction(mode, initial.id, body),
    retry: false,
    onSuccess: (_response, body) => {
      actionRef.current = null
      setNote('')
      setCloseOpen(false)
      if (body.action === 'deliver') {
        setText('')
        setFiles([])
      }
      invalidate()
    },
  })
  const send = useMutation({
    mutationFn: (body: MessageOutbox) =>
      sendDemandMessage(mode, initial.id, body),
    retry: false,
    onSuccess: () => {
      try {
        sessionStorage.removeItem(outboxKey)
      } catch {
        /* The retained key remains safe to replay. */
      }
      setOutbox(null)
      setText('')
      setFiles([])
      invalidate()
    },
  })
  const earlier = useMutation({
    retry: false,
    mutationFn: () =>
      loadConversation(
        mode,
        initial.id,
        Math.max(0, (conversation.data?.items[0]?.seq ?? 1) - 101)
      ),
    onSuccess: (response) => {
      cache.setQueryData<ConversationData>(key, (old) =>
        old
          ? { ...old, items: mergeMessages(response.items, old.items) }
          : response
      )
    },
  })
  const lastRead = useRef(0)
  const lastSeq = conversation.data?.last_seq ?? 0
  const read = useMutation({
    mutationFn: (seq: number) => readDemand(mode, initial.id, seq),
    retry: false,
    onSuccess: (_result, seq) => {
      lastRead.current = seq
      void cache.invalidateQueries({ queryKey: ['demands', accountId] })
    },
  })
  useEffect(() => {
    if (
      lastSeq > lastRead.current &&
      !read.isPending &&
      !read.isError &&
      document.visibilityState === 'visible'
    ) {
      read.mutate(lastSeq)
    }
  }, [mode, lastSeq, read])
  const demand = conversation.data?.demand ?? initial
  const terminal = ['completed', 'cancelled', 'closed'].includes(demand.status)
  const runAction = (name: string, extra: Record<string, unknown> = {}) => {
    const signature = JSON.stringify({
      action: name,
      revision: demand.revision,
      ...extra,
    })
    if (actionRef.current?.signature !== signature) {
      actionRef.current = {
        signature,
        body: {
          client_action_id: crypto.randomUUID(),
          action: name,
          revision: demand.revision,
          ...extra,
        },
      }
    }
    action.mutate(actionRef.current.body)
  }
  const upload = async (draft: AttachmentDraft) => {
    const controller = new AbortController()
    uploads.current.set(draft.key, controller)
    setFiles((old) =>
      old.map((v) => (v.key === draft.key ? { ...v, failed: false } : v))
    )
    try {
      const attachment = await uploadDemandFile(
        draft.file,
        draft.key,
        (progress) =>
          setFiles((old) =>
            old.map((v) => (v.key === draft.key ? { ...v, progress } : v))
          ),
        mode,
        controller.signal
      )
      setFiles((old) =>
        old.map((v) =>
          v.key === draft.key ? { ...v, attachment, progress: 100 } : v
        )
      )
    } catch {
      if (!controller.signal.aborted) {
        setFiles((old) =>
          old.map((v) => (v.key === draft.key ? { ...v, failed: true } : v))
        )
      }
    } finally {
      uploads.current.delete(draft.key)
    }
  }
  const submit = () => {
    const body = outbox ?? {
      client_message_id: crypto.randomUUID(),
      text,
      attachment_ids: files.flatMap((f) =>
        f.attachment ? [f.attachment.id] : []
      ),
    }
    // Persist the exact message fence before any POST; a remount never invents a retry key.
    try {
      sessionStorage.setItem(outboxKey, JSON.stringify(body))
    } catch {
      setLocalError(
        'Unable to save the submission receipt locally. Nothing was sent.'
      )
      return
    }
    setLocalError('')
    setOutbox(body)
    send.mutate(body)
  }
  let sendLabel = mode === 'admin' ? 'Send to customer' : 'Send'
  if (outbox) sendLabel = 'Retry same message'
  if (send.isPending) sendLabel = 'Sending...'
  const ready = files.every((f) => f.attachment !== undefined)
  return (
    <section
      className='flex h-full min-h-0 flex-col'
      aria-label={t('Demand conversation')}
    >
      <header className='shrink-0 space-y-3 border-b p-4'>
        <Button variant='ghost' onClick={onBack} className='md:hidden'>
          {t('Back to demands')}
        </Button>
        <div className='flex flex-wrap items-start justify-between gap-2'>
          <div className='min-w-0'>
            <h2 className='font-medium break-words'>{demand.title}</h2>
            <p className='text-muted-foreground mt-1 text-xs break-all'>
              {demand.number}
            </p>
          </div>
          <Badge variant='secondary'>{t(statusLabels[demand.status])}</Badge>
        </div>
        {mode === 'admin' ? (
          <section aria-label={t('Customer information')} className='space-y-2'>
            <h3 className='text-sm font-medium'>{t('Customer information')}</h3>
            <dl className='grid gap-2 text-sm sm:grid-cols-3'>
              <div>
                <dt className='text-muted-foreground text-xs'>
                  {t('Nickname')}
                </dt>
                <dd className='break-words'>
                  {demand.owner?.nickname || t('Not provided')}
                </dd>
              </div>
              <div>
                <dt className='text-muted-foreground text-xs'>
                  {t('Phone number')}
                </dt>
                <dd className='break-all'>
                  {demand.owner?.phone || t('Not provided')}
                </dd>
              </div>
              <div>
                <dt className='text-muted-foreground text-xs'>
                  {t('User ID')}
                </dt>
                <dd>{demand.owner?.id ?? demand.user_id}</dd>
              </div>
            </dl>
          </section>
        ) : (
          <p className='text-muted-foreground text-sm'>
            {demand.engineer.display_name} ·{' '}
            {demand.owner?.nickname ?? t('User')} {demand.owner?.phone ?? ''}
          </p>
        )}
        <div className='flex flex-wrap gap-2'>
          {mode === 'engineer' && !terminal && (
            <>
              {demand.status === 'pending' && (
                <Button
                  disabled={action.isPending}
                  onClick={() => runAction('accept')}
                >
                  {t('Accept demand')}
                </Button>
              )}
              {demand.status === 'communicating' && (
                <Button
                  disabled={action.isPending}
                  onClick={() => runAction('start')}
                >
                  {t('Start work')}
                </Button>
              )}
              <Button
                variant='outline'
                disabled={action.isPending}
                onClick={() =>
                  runAction('request_transfer', {
                    text: t('Engineer requested reassignment'),
                  })
                }
              >
                {t('Request reassignment')}
              </Button>
            </>
          )}
          {mode === 'admin' && canWrite && !terminal && (
            <>
              <NativeSelect
                aria-label={t('Assign engineer')}
                value={assignment}
                onChange={(e) => setAssignment(e.target.value)}
              >
                <option value=''>{t('Assign engineer')}</option>
                {engineers
                  .filter((e) => e.enabled && e.id !== demand.engineer_id)
                  .map((e) => (
                    <option value={e.id} key={e.id}>
                      {e.display_name}
                    </option>
                  ))}
              </NativeSelect>
              <Button
                variant='outline'
                disabled={!assignment || action.isPending}
                onClick={() => runAction('assign', { engineer_id: assignment })}
              >
                {t('Reassign')}
              </Button>
              <NativeSelect
                aria-label={t('Priority')}
                value={demand.priority}
                disabled={action.isPending}
                onChange={(e) =>
                  runAction('priority', { priority: e.target.value })
                }
              >
                {['normal', 'high', 'urgent'].map((v) => (
                  <option key={v} value={v}>
                    {t(
                      (
                        {
                          normal: 'Normal',
                          high: 'High',
                          urgent: 'Urgent',
                        } as Record<string, string>
                      )[v]
                    )}
                  </option>
                ))}
              </NativeSelect>
              <NativeSelect
                aria-label={t('Adjust demand status')}
                value=''
                disabled={action.isPending}
                onChange={(e) => {
                  if (e.target.value) {
                    runAction('status', { status: e.target.value })
                  }
                }}
              >
                <option value=''>{t('Adjust demand status')}</option>
                {['pending', 'communicating', 'in_progress'].map((value) => (
                  <option key={value} value={value}>
                    {t(statusLabels[value as keyof typeof statusLabels])}
                  </option>
                ))}
              </NativeSelect>
              <Button
                variant='outline'
                disabled={action.isPending}
                onClick={() => setCloseOpen(true)}
              >
                {t('Close demand')}
              </Button>
            </>
          )}
        </div>
      </header>
      <div
        className='min-h-0 flex-1 space-y-4 overflow-y-auto p-4'
        aria-live='polite'
        aria-relevant='additions'
      >
        {conversation.isPending && <LoadingState />}
        {conversation.isError && (
          <p role='alert' className='text-destructive text-sm'>
            {t(
              'Messages could not be refreshed. Existing messages are retained.'
            )}
          </p>
        )}
        {(conversation.data?.items[0]?.seq ?? 1) > 1 && (
          <Button
            variant='outline'
            disabled={earlier.isPending}
            onClick={() => earlier.mutate()}
          >
            {t('Load earlier messages')}
          </Button>
        )}
        {earlier.isError && (
          <p role='alert'>{t('Earlier messages could not be loaded')}</p>
        )}
        {conversation.data?.items.map((message) => (
          <article key={message.id} className='space-y-2 border-b pb-4'>
            <div className='text-muted-foreground flex flex-wrap gap-2 text-xs'>
              <span>
                {message.author_name?.trim() ||
                  t(
                    {
                      user: 'User',
                      engineer: 'Engineer',
                      system: 'Administrator',
                    }[message.author]
                  )}
              </span>
              <time>{formatTimestampToDate(message.created_at)}</time>
              {message.kind === 'delivery' && (
                <Badge variant='outline'>{t('Delivery')}</Badge>
              )}
              {message.read && <span>{t('Read')}</span>}
            </div>
            <p className='text-sm break-words whitespace-pre-wrap'>
              {message.text}
            </p>
            <div className='grid gap-3 sm:grid-cols-2'>
              {message.attachments.map((attachment) => (
                <DemandMedia
                  key={attachment.id}
                  attachment={attachment}
                  mode={mode}
                />
              ))}
            </div>
          </article>
        ))}
        {mode === 'admin' && audit.data && (
          <section
            className='space-y-2 rounded-lg border p-3'
            aria-label={t('Internal timeline')}
          >
            <h3 className='font-medium'>{t('Internal timeline')}</h3>
            <p className='text-muted-foreground text-xs'>
              {t('Internal notes are visible only to administrators.')}
            </p>
            {audit.data.items.map((event) => (
              <div key={event.id} className='border-b py-2 text-sm'>
                <span className='text-muted-foreground text-xs'>
                  {formatTimestampToDate(event.created_at)} · {event.action}
                </span>
                <p className='break-words whitespace-pre-wrap'>
                  {event.detail}
                </p>
              </div>
            ))}
            {audit.data.has_more && (
              <Button
                variant='outline'
                disabled={audit.isFetching}
                onClick={() => {
                  void audit.refetch()
                }}
              >
                {t('Load more')}
              </Button>
            )}
          </section>
        )}
      </div>
      {mode === 'admin' && canWrite && (
        <div
          role='group'
          aria-label={t('Message visibility')}
          className='flex shrink-0 gap-2 border-t px-4 pt-3'
        >
          <Button
            variant={!terminal && composer === 'reply' ? 'secondary' : 'ghost'}
            aria-pressed={!terminal && composer === 'reply'}
            disabled={terminal || send.isPending}
            onClick={() => setComposer('reply')}
          >
            {t('Reply to customer')}
          </Button>
          <Button
            variant={terminal || composer === 'note' ? 'secondary' : 'ghost'}
            aria-pressed={terminal || composer === 'note'}
            disabled={send.isPending}
            onClick={() => setComposer('note')}
          >
            {t('Internal note')}
          </Button>
        </div>
      )}
      {canWrite &&
        !terminal &&
        (mode === 'engineer' || composer === 'reply') && (
          <footer className='shrink-0 space-y-3 border-t p-4'>
            {mode === 'admin' && (
              <p className='text-muted-foreground text-xs'>
                {t(
                  'This reply is visible to the customer. No engineer assignment is required.'
                )}
              </p>
            )}
            {outbox && (
              <p role='status' className='text-muted-foreground text-sm'>
                {t(
                  'An unconfirmed message is retained. Retry sends the same message, not a new one.'
                )}
              </p>
            )}
            <label className='block text-sm' htmlFor={`reply-${initial.id}`}>
              {t(
                mode === 'admin' ? 'Reply to customer' : 'Message to customer'
              )}
            </label>
            <Textarea
              id={`reply-${initial.id}`}
              value={text}
              maxLength={10000}
              disabled={!!outbox || send.isPending}
              onChange={(e) => setText(e.target.value)}
            />
            <label className='block text-sm'>
              {t('Add images or videos')}
              <Input
                type='file'
                multiple
                accept={rules.data?.allowed_mime_types.join(',')}
                disabled={!!outbox || send.isPending || !rules.data}
                onChange={(e) => {
                  const candidates = [...(e.target.files ?? [])]
                  const r = rules.data
                  if (
                    !r ||
                    files.length + candidates.length > r.max_attachments ||
                    candidates.some(
                      (file) =>
                        !r.allowed_mime_types.includes(file.type) ||
                        file.size >
                          (file.type.startsWith('image/')
                            ? r.max_image_bytes
                            : r.max_video_bytes)
                    )
                  ) {
                    setLocalError(
                      'These files exceed the configured attachment rules.'
                    )
                    e.target.value = ''
                    return
                  }
                  setLocalError('')
                  const selected = candidates.map((file) => ({
                    key: crypto.randomUUID(),
                    file,
                    progress: 0,
                    failed: false,
                  }))
                  setFiles((old) => [...old, ...selected])
                  for (const draft of selected) void upload(draft)
                  e.target.value = ''
                }}
              />
            </label>
            {rules.data && (
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Up to {{count}} attachments; images {{image}} MiB, videos {{video}} MiB.',
                  {
                    count: rules.data.max_attachments,
                    image: rules.data.max_image_bytes / 1048576,
                    video: rules.data.max_video_bytes / 1048576,
                  }
                )}
              </p>
            )}
            {localError && (
              <p role='alert' className='text-destructive text-sm'>
                {t(localError)}
              </p>
            )}
            {rules.isError && (
              <p role='alert'>{t('Attachment rules could not be loaded')}</p>
            )}
            {files.map((file) => (
              <div
                key={file.key}
                className='flex items-center justify-between gap-2 text-xs'
              >
                <span className='min-w-0 truncate'>
                  {file.file.name} · {file.progress}%
                </span>
                {file.failed && (
                  <Button
                    variant='outline'
                    onClick={() => {
                      void upload(file)
                    }}
                  >
                    {t('Retry upload')}
                  </Button>
                )}
                <Button
                  variant='ghost'
                  disabled={!!outbox || send.isPending}
                  aria-label={`${t('Remove attachment')} ${file.file.name}`}
                  onClick={() => {
                    uploads.current.get(file.key)?.abort()
                    setFiles((old) =>
                      old.filter((value) => value.key !== file.key)
                    )
                  }}
                >
                  {t('Remove')}
                </Button>
              </div>
            ))}
            <div className='flex flex-wrap gap-2'>
              <Button
                disabled={
                  send.isPending ||
                  (!outbox && (!ready || (!text.trim() && files.length === 0)))
                }
                onClick={submit}
              >
                {t(sendLabel)}
              </Button>
              {mode === 'engineer' &&
                ['communicating', 'in_progress'].includes(demand.status) && (
                  <Button
                    variant='outline'
                    disabled={
                      !!outbox ||
                      !ready ||
                      action.isPending ||
                      (!text.trim() && files.length === 0)
                    }
                    onClick={() =>
                      runAction('deliver', {
                        text,
                        attachment_ids: files.flatMap((f) =>
                          f.attachment ? [f.attachment.id] : []
                        ),
                      })
                    }
                  >
                    {t('Submit delivery')}
                  </Button>
                )}
            </div>
            {send.isError && (
              <p role='alert' className='text-destructive text-sm'>
                {t(
                  'Message delivery is unconfirmed. Your draft and submission key are retained.'
                )}
              </p>
            )}
          </footer>
        )}
      {mode === 'admin' && canWrite && (terminal || composer === 'note') && (
        <footer className='shrink-0 space-y-2 border-t p-4'>
          <label htmlFor={`note-${initial.id}`} className='text-sm'>
            {t('Internal note')}
          </label>
          <Textarea
            id={`note-${initial.id}`}
            value={note}
            onChange={(e) => setNote(e.target.value)}
            maxLength={10000}
          />
          <Button
            disabled={!note.trim() || action.isPending}
            onClick={() => runAction('internal_note', { text: note })}
          >
            {t('Add internal note')}
          </Button>
        </footer>
      )}
      {action.isError && (
        <p role='alert' className='text-destructive px-4 pb-3 text-sm'>
          {t(
            'The action was not confirmed. Refresh the demand before changing its state.'
          )}
        </p>
      )}
      <ConfirmDialog
        open={closeOpen}
        onOpenChange={setCloseOpen}
        title={t('Close demand?')}
        desc={t(
          'The conversation and attachments will be retained. New messages will be disabled.'
        )}
        confirmText={t('Close demand')}
        isLoading={action.isPending}
        handleConfirm={() => runAction('close')}
      />
    </section>
  )
}

function DemandMedia({
  attachment,
  mode,
}: {
  attachment: Attachment
  mode: DemandMode
}) {
  const { t } = useTranslation()
  const [preview, setPreview] = useState<string | null>(null)
  const [expired, setExpired] = useState(false)
  const media = useRef<HTMLVideoElement>(null)
  const resume = useRef(0)
  const link = useMutation({
    mutationFn: (disposition: 'inline' | 'attachment') =>
      attachmentLink(attachment.id, disposition, mode),
    retry: false,
    onSuccess: (result, disposition) => {
      if (disposition === 'inline') {
        setPreview(result.url)
        setExpired(false)
      } else {
        const anchor = document.createElement('a')
        anchor.href = result.url
        anchor.download = attachment.name
        anchor.rel = 'noreferrer'
        anchor.click()
      }
    },
  })
  return (
    <div className='min-w-0 space-y-2 rounded-lg border p-3'>
      <p className='truncate text-sm'>{attachment.name}</p>
      <div className='flex flex-wrap gap-2'>
        <Button
          size='sm'
          variant='outline'
          disabled={link.isPending}
          onClick={() => link.mutate('inline')}
        >
          {t('Preview')}
        </Button>
        <Button
          size='sm'
          variant='ghost'
          disabled={link.isPending}
          onClick={() => link.mutate('attachment')}
        >
          {t('Download original')}
        </Button>
      </div>
      {link.isError && (
        <p role='alert' className='text-destructive text-xs'>
          {t('Unable to authorize this attachment')}
        </p>
      )}
      {preview &&
        (attachment.kind === 'image' ? (
          <a href={preview} target='_blank' rel='noreferrer'>
            <img
              src={preview}
              alt={attachment.name}
              className='max-h-80 w-full rounded object-contain'
              loading='lazy'
              onError={() => {
                setPreview(null)
                setExpired(true)
              }}
            />
          </a>
        ) : (
          <video
            ref={media}
            src={preview}
            controls
            preload='metadata'
            className='max-h-80 w-full rounded'
            onTimeUpdate={() => {
              if (media.current) resume.current = media.current.currentTime
            }}
            onLoadedMetadata={() => {
              if (media.current && resume.current > 0) {
                media.current.currentTime = resume.current
              }
            }}
            onError={() => {
              setPreview(null)
              setExpired(true)
            }}
          />
        ))}
      {expired && (
        <p role='status' className='text-muted-foreground text-xs'>
          {t('Attachment preview expired. Request a new link.')}
        </p>
      )}
      {!preview && attachment.kind === 'video' && expired && (
        <Button variant='outline' onClick={() => link.mutate('inline')}>
          {t('Renew video access')}
        </Button>
      )}
    </div>
  )
}
