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
import { z } from 'zod'

import {
  Conversation,
  ConversationContent,
  ConversationScrollButton,
} from '@/components/ai-elements/conversation'
import { ConfirmDialog } from '@/components/confirm-dialog'
import { sideDrawerContentClassName } from '@/components/drawer-layout'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from '@/components/ui/sheet'
import { Textarea } from '@/components/ui/textarea'
import { formatTimestampToDate } from '@/lib/format'
import { getServerErrorMessage } from '@/lib/server-error-message'
import { cn } from '@/lib/utils'

import {
  communityAttachmentLink,
  communityStatusLabels,
  loadCommunityAudit,
  loadCommunityConfig,
  loadCommunityConversation,
  moderateCommunity,
  sendCommunityMessage,
  uploadCommunityFile,
  type CommunityAttachment,
  type CommunityMessage,
  type CommunityModeration,
  type CommunityOrder,
} from './community-api'

const messagePacket = z.object({
  client_message_id: z.string(),
  text: z.string(),
  attachment_ids: z.array(z.string()),
})
const actionPacket = z.object({
  client_action_id: z.string(),
  revision: z.number(),
  action: z.enum(['hide_listing', 'show_listing', 'cancel', 'reopen']),
  reason: z.string(),
})
function restore<T>(key: string, schema: z.ZodType<T>): T | null {
  try {
    const raw = sessionStorage.getItem(key)
    return raw ? schema.parse(JSON.parse(raw)) : null
  } catch {
    return null
  }
}
function merge(a: CommunityMessage[], b: CommunityMessage[]) {
  return [...new Map([...a, ...b].map((m) => [m.id, m])).values()].sort(
    (x, y) => x.seq - y.seq
  )
}
type ConversationData = Awaited<ReturnType<typeof loadCommunityConversation>>
type Draft = {
  key: string
  file: File
  attachment?: CommunityAttachment
  failed: boolean
  busy: boolean
}

export function CommunityConversation(props: {
  initial: CommunityOrder
  accountId: number
  canWrite: boolean
  onBack: () => void
}) {
  const { t } = useTranslation()
  const cache = useQueryClient()
  const id = props.initial.id
  const key = ['community-conversation', props.accountId, id]
  const messageKey = `community-admin-message:${props.accountId}:${id}`
  const actionKey = `community-admin-action:${props.accountId}:${id}`
  const [outbox, setOutbox] = useState(() => restore(messageKey, messagePacket))
  const [pendingAction, setPendingAction] = useState(() =>
    restore(actionKey, actionPacket)
  )
  const [text, setText] = useState(outbox?.text ?? '')
  const [files, setFiles] = useState<Draft[]>([])
  const [confirm, setConfirm] = useState<CommunityModeration['action'] | null>(
    null
  )
  const [reason, setReason] = useState('')
  const [localError, setLocalError] = useState('')
  const input = useRef<HTMLInputElement>(null)
  const uploads = useRef(new Set<AbortController>())
  useEffect(() => {
    const controllers = uploads.current
    return () => controllers.forEach((c) => c.abort())
  }, [])
  const data = useQuery({
    queryKey: key,
    retry: false,
    refetchInterval: 3000,
    queryFn: async ({ signal }) => {
      const previous = cache.getQueryData<ConversationData>(key)
      let response = await loadCommunityConversation(
        id,
        previous?.items.at(-1)?.seq ??
          Math.max(0, props.initial.last_seq - 100),
        signal
      )
      let items = merge(previous?.items ?? [], response.items)
      for (let page = 0; response.has_more && page < 4; page++) {
        response = await loadCommunityConversation(
          id,
          response.next_after_seq,
          signal
        )
        items = merge(items, response.items)
      }
      return { ...response, items }
    },
  })
  const config = useQuery({
    queryKey: ['community-config', props.accountId],
    queryFn: ({ signal }) => loadCommunityConfig(signal),
    retry: false,
  })
  const audit = useQuery({
    queryKey: ['community-audit', props.accountId, id],
    queryFn: ({ signal }) => loadCommunityAudit(id, signal),
    retry: false,
    refetchInterval: 15000,
  })
  const order = data.data?.order ?? props.initial
  const refresh = () => {
    void cache.invalidateQueries({
      queryKey: ['community-orders', props.accountId],
    })
    void cache.invalidateQueries({ queryKey: key })
    void cache.invalidateQueries({
      queryKey: ['community-audit', props.accountId, id],
    })
  }
  const applyReceipt = (
    receipt: Awaited<ReturnType<typeof sendCommunityMessage>>
  ) => {
    cache.setQueryData<ConversationData>(key, (prior) =>
      prior
        ? {
            ...prior,
            order: receipt.order,
            last_seq: receipt.order.last_seq,
            items: merge(prior.items, receipt.message ? [receipt.message] : []),
          }
        : prior
    )
    refresh()
  }
  const send = useMutation({
    mutationFn: (body: z.infer<typeof messagePacket>) =>
      sendCommunityMessage(id, body),
    retry: false,
    onSuccess: (receipt) => {
      sessionStorage.removeItem(messageKey)
      setOutbox(null)
      setText('')
      setFiles([])
      applyReceipt(receipt)
    },
  })
  const action = useMutation({
    mutationFn: (body: CommunityModeration) => moderateCommunity(id, body),
    retry: false,
    onSuccess: (receipt) => {
      sessionStorage.removeItem(actionKey)
      setPendingAction(null)
      setConfirm(null)
      setReason('')
      applyReceipt(receipt)
    },
  })
  const earlier = useMutation({
    mutationFn: () =>
      loadCommunityConversation(
        id,
        Math.max(0, (data.data?.items[0]?.seq ?? 1) - 101)
      ),
    retry: false,
    onSuccess: (page) =>
      cache.setQueryData<ConversationData>(key, (prior) =>
        prior ? { ...prior, items: merge(page.items, prior.items) } : page
      ),
  })
  const busy = send.isPending || action.isPending
  const locked =
    busy || !!outbox || !!pendingAction || !order.can_send || !props.canWrite
  const uploadFile = async (draft: Draft) => {
    const controller = new AbortController()
    uploads.current.add(controller)
    setFiles((old) =>
      old.map((f) =>
        f.key === draft.key ? { ...f, busy: true, failed: false } : f
      )
    )
    try {
      const attachment = await uploadCommunityFile(
        id,
        draft.file,
        draft.key,
        controller.signal
      )
      setFiles((old) =>
        old.map((f) =>
          f.key === draft.key ? { ...f, attachment, busy: false } : f
        )
      )
    } catch {
      if (!controller.signal.aborted) {
        setFiles((old) =>
          old.map((f) =>
            f.key === draft.key ? { ...f, failed: true, busy: false } : f
          )
        )
      }
    } finally {
      uploads.current.delete(controller)
    }
  }
  const selectFiles = (selected: FileList | null) => {
    if (!selected || !config.data) return
    const rules = config.data.upload_rules
    if (selected.length + files.length > rules.max_attachments) {
      setLocalError(t('Too many attachments'))
      return
    }
    const next: Draft[] = []
    for (const file of selected) {
      const max = file.type.startsWith('image/')
        ? rules.max_image_bytes
        : rules.max_video_bytes
      if (!rules.allowed_mime_types.includes(file.type) || file.size > max) {
        setLocalError(t('Attachment type or size is not allowed'))
        return
      }
      next.push({ key: crypto.randomUUID(), file, failed: false, busy: true })
    }
    setLocalError('')
    setFiles((old) => [...old, ...next])
    next.forEach((f) => void uploadFile(f))
  }
  const submit = () => {
    setLocalError('')
    const body = outbox ?? {
      client_message_id: crypto.randomUUID(),
      text,
      attachment_ids: files.flatMap((f) =>
        f.attachment ? [f.attachment.id] : []
      ),
    }
    try {
      sessionStorage.setItem(messageKey, JSON.stringify(body))
    } catch {
      setLocalError(t('Unable to preserve the send receipt'))
      return
    }
    setOutbox(body)
    send.mutate(body)
  }
  const moderate = () => {
    if (!confirm) return
    const body = pendingAction ?? {
      client_action_id: crypto.randomUUID(),
      revision: order.revision,
      action: confirm,
      reason,
    }
    try {
      sessionStorage.setItem(actionKey, JSON.stringify(body))
    } catch {
      setLocalError(t('Unable to preserve the send receipt'))
      return
    }
    setPendingAction(body)
    action.mutate(body)
  }
  return (
    <section
      aria-label={t('Community order conversation')}
      className='flex h-full min-h-0 flex-col'
    >
      <header className='flex shrink-0 items-center gap-3 border-b px-4 py-3'>
        <Button
          variant='ghost'
          size='sm'
          className='md:hidden'
          onClick={props.onBack}
        >
          {t('Back')}
        </Button>
        <div className='min-w-0 flex-1'>
          <h2 className='truncate text-sm font-medium'>{order.title}</h2>
          <p className='text-muted-foreground mt-1 text-xs'>
            {order.publisher.display_name || t('Publisher')} →{' '}
            {order.acceptor?.display_name || t('Not accepted')}
          </p>
        </div>
        <Badge variant='secondary'>
          {t(communityStatusLabels[order.status])}
        </Badge>
        <Sheet>
          <SheetTrigger render={<Button size='sm' variant='ghost' />}>
            {t('Order details')}
          </SheetTrigger>
          <SheetContent className={sideDrawerContentClassName()}>
            <SheetHeader>
              <SheetTitle>{t('Order details')}</SheetTitle>
            </SheetHeader>
            <div className='min-h-0 space-y-5 overflow-y-auto px-5 py-4 text-sm'>
              <p className='text-muted-foreground text-xs break-all'>
                {order.number}
              </p>
              {[
                { label: t('Publisher'), user: order.publisher },
                { label: t('Accepting user'), user: order.acceptor },
              ].map(({ label, user }) => (
                <div key={label}>
                  <h3 className='mb-1 font-medium'>{label}</h3>
                  {user ? (
                    <>
                      <p>
                        {user.display_name || t('User')} · ID {user.id}
                      </p>
                      <p className='text-muted-foreground'>
                        {user.phone_e164 || t('No phone number')}
                      </p>
                    </>
                  ) : (
                    <p>{t('Not accepted')}</p>
                  )}
                </div>
              ))}
              <div>
                <h3 className='mb-1 font-medium'>{t('Public overview')}</h3>
                <p className='whitespace-pre-wrap'>
                  {order.listing.public_summary}
                </p>
                {order.listing.public_budget && (
                  <p>{order.listing.public_budget}</p>
                )}
                {order.listing.service_profile_name && (
                  <p>{order.listing.service_profile_name}</p>
                )}
                {order.listing.public_attachments.map((f) => (
                  <CommunityMedia key={f.id} attachment={f} />
                ))}
              </div>
              <div>
                <h3 className='mb-1 font-medium'>{t('Private request')}</h3>
                <p className='whitespace-pre-wrap'>
                  {order.private_text || '—'}
                </p>
              </div>
              {props.canWrite && (
                <div className='flex flex-wrap gap-2'>
                  <Button
                    size='sm'
                    variant='outline'
                    disabled={busy || !!pendingAction}
                    onClick={() =>
                      setConfirm(
                        order.listing_visible ? 'hide_listing' : 'show_listing'
                      )
                    }
                  >
                    {t(
                      order.listing_visible
                        ? 'Hide public listing'
                        : 'Restore public listing'
                    )}
                  </Button>
                  {order.can_cancel && (
                    <Button
                      size='sm'
                      variant='destructive'
                      disabled={busy || !!pendingAction}
                      onClick={() => setConfirm('cancel')}
                    >
                      {t('Cancel order')}
                    </Button>
                  )}
                  {order.can_reopen && (
                    <Button
                      size='sm'
                      variant='outline'
                      disabled={busy || !!pendingAction}
                      onClick={() => setConfirm('reopen')}
                    >
                      {t('Reopen order')}
                    </Button>
                  )}
                </div>
              )}
              <div>
                <h3 className='mb-2 font-medium'>{t('Audit history')}</h3>
                {audit.data?.items.map((event) => (
                  <p
                    key={event.id}
                    className='text-muted-foreground mb-2 text-xs'
                  >
                    {formatTimestampToDate(event.created_at)} · ID{' '}
                    {event.actor_id} · {event.action}
                    <br />
                    {event.reason}
                  </p>
                ))}
                {audit.isError && (
                  <Button
                    size='sm'
                    variant='ghost'
                    onClick={() => void audit.refetch()}
                  >
                    {t('Retry')}
                  </Button>
                )}
              </div>
            </div>
          </SheetContent>
        </Sheet>
      </header>
      {data.isPending && <LoadingState />}
      {data.isError && !data.data && (
        <ErrorState onRetry={() => void data.refetch()} />
      )}
      <Conversation>
        <ConversationContent className='space-y-4 p-4'>
          {(data.data?.items[0]?.seq ?? 1) > 1 && (
            <Button
              variant='ghost'
              size='sm'
              disabled={earlier.isPending}
              onClick={() => earlier.mutate()}
            >
              {t('Earlier messages')}
            </Button>
          )}
          {data.data?.items.map((message) =>
            message.author_kind === 'system' ? (
              <p
                key={message.id}
                className='text-muted-foreground text-center text-xs'
              >
                {message.text || t('Order updated')}
              </p>
            ) : (
              <article
                key={message.id}
                className={cn(
                  'flex',
                  message.sender_id === props.accountId
                    ? 'justify-end'
                    : 'justify-start'
                )}
              >
                <div className='max-w-[90%] min-w-0 sm:max-w-[80%]'>
                  <p className='text-muted-foreground mb-1 text-xs'>
                    {message.author_name ||
                      t(
                        message.author_kind === 'admin'
                          ? 'Administrator'
                          : 'User'
                      )}{' '}
                    · {t(authorLabel(message.author_kind))}
                  </p>
                  <div
                    className={cn(
                      'space-y-2 rounded-xl px-3.5 py-3',
                      message.sender_id === props.accountId
                        ? 'bg-primary/10'
                        : 'bg-muted/60'
                    )}
                  >
                    {message.kind === 'delivery' && (
                      <Badge variant='outline'>{t('Delivery')}</Badge>
                    )}
                    <p className='text-sm leading-6 break-words whitespace-pre-wrap'>
                      {message.text}
                    </p>
                    {message.attachments.map((file) => (
                      <CommunityMedia key={file.id} attachment={file} />
                    ))}
                  </div>
                  <time className='text-muted-foreground mt-1 block text-[11px]'>
                    {formatTimestampToDate(message.created_at)}
                  </time>
                </div>
              </article>
            )
          )}
        </ConversationContent>
        <ConversationScrollButton aria-label={t('Scroll to bottom')} />
      </Conversation>
      {props.canWrite && (
        <div className='shrink-0 space-y-2 border-t p-3'>
          <Textarea
            aria-label={t('Reply to both participants')}
            placeholder={t('Reply to both participants')}
            value={text}
            maxLength={12000}
            disabled={locked}
            onChange={(e) => setText(e.target.value)}
            className='min-h-20 resize-none'
          />
          {files.map((file) => (
            <div key={file.key} className='flex items-center gap-2 text-xs'>
              <span className='truncate'>{file.file.name}</span>
              <span>{t(uploadLabel(file))}</span>
              {file.failed && (
                <Button
                  size='sm'
                  variant='ghost'
                  onClick={() => void uploadFile(file)}
                >
                  {t('Retry upload')}
                </Button>
              )}
              {!file.busy && !outbox && (
                <Button
                  size='sm'
                  variant='ghost'
                  onClick={() =>
                    setFiles((old) => old.filter((f) => f.key !== file.key))
                  }
                >
                  {t('Remove')}
                </Button>
              )}
            </div>
          ))}
          <div className='flex items-center gap-2'>
            <Input
              type='file'
              ref={input}
              className='hidden'
              multiple
              accept={config.data?.upload_rules.allowed_mime_types.join(',')}
              onChange={(e) => {
                selectFiles(e.target.files)
                e.target.value = ''
              }}
            />
            <Button
              size='sm'
              variant='ghost'
              disabled={locked || !config.data}
              onClick={() => input.current?.click()}
            >
              {t('Add image or video')}
            </Button>
            <span className='text-muted-foreground min-w-0 flex-1 text-xs'>
              {!order.can_send && t('Order is read-only')}
            </span>
            <Button
              size='sm'
              disabled={
                busy ||
                !!pendingAction ||
                (!outbox &&
                  (!order.can_send ||
                    (!text.trim() && files.length === 0) ||
                    files.some((f) => !f.attachment)))
              }
              onClick={submit}
            >
              {t(outbox ? 'Retry same message' : 'Send')}
            </Button>
          </div>
          {pendingAction && (
            <Button
              size='sm'
              variant='outline'
              disabled={busy}
              onClick={() => action.mutate(pendingAction)}
            >
              {t('Retry same action')}
            </Button>
          )}
          {(localError || send.isError || action.isError) && (
            <p role='alert' className='text-destructive text-xs'>
              {localError ||
                getServerErrorMessage(
                  send.error || action.error,
                  t('The result is uncertain. Retry with the original receipt.')
                )}
            </p>
          )}
        </div>
      )}
      <ConfirmDialog
        open={!!confirm}
        onOpenChange={(open) => {
          if (!open && !action.isPending) setConfirm(null)
        }}
        title={t(
          confirm === 'cancel' ? 'Cancel order' : 'Change listing visibility'
        )}
        desc={t('This action is recorded and applies immediately.')}
        confirmText={t('Confirm')}
        destructive={confirm === 'cancel' || confirm === 'hide_listing'}
        disabled={reason.trim().length === 0}
        isLoading={action.isPending}
        handleConfirm={moderate}
      >
        <Textarea
          aria-label={t('Moderation reason')}
          placeholder={t('Moderation reason')}
          maxLength={1000}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          disabled={!!pendingAction}
        />
        {action.isError && (
          <p role='alert' className='text-destructive text-xs'>
            {getServerErrorMessage(
              action.error,
              t('The result is uncertain. Retry with the original receipt.')
            )}
          </p>
        )}
      </ConfirmDialog>
    </section>
  )
}

function uploadLabel(file: Draft) {
  if (file.attachment) return 'Ready'
  if (file.failed) return 'Upload failed'
  return 'Uploading'
}

function authorLabel(kind: CommunityMessage['author_kind']) {
  if (kind === 'acceptor') return 'Accepting user'
  if (kind === 'publisher') return 'Publisher'
  return 'Administrator'
}

function CommunityMedia(props: { attachment: CommunityAttachment }) {
  const { t } = useTranslation()
  const [preview, setPreview] = useState('')
  const media = useRef<HTMLVideoElement>(null)
  const position = useRef(0)
  const link = useMutation({
    mutationFn: (disposition: 'inline' | 'attachment') =>
      communityAttachmentLink(props.attachment.id, disposition),
    retry: false,
    onSuccess: (result, disposition) => {
      if (disposition === 'inline') setPreview(result.url)
      else {
        const anchor = document.createElement('a')
        anchor.href = result.url
        anchor.download = props.attachment.name
        anchor.rel = 'noreferrer'
        anchor.click()
      }
    },
  })
  return (
    <div className='space-y-1.5'>
      <p className='truncate text-xs'>{props.attachment.name}</p>
      <div className='flex gap-1'>
        <Button
          variant='outline'
          size='sm'
          disabled={link.isPending}
          onClick={() => link.mutate('inline')}
        >
          {t(preview ? 'Refresh preview' : 'Preview')}
        </Button>
        <Button
          variant='ghost'
          size='sm'
          disabled={link.isPending}
          onClick={() => link.mutate('attachment')}
        >
          {t('Download original')}
        </Button>
      </div>
      {preview &&
        (props.attachment.kind === 'image' ? (
          <img
            src={preview}
            alt={props.attachment.name}
            className='max-h-80 max-w-full rounded object-contain'
            onError={() => setPreview('')}
          />
        ) : (
          <video
            ref={media}
            src={preview}
            controls
            preload='metadata'
            className='max-h-80 max-w-full rounded'
            onTimeUpdate={() => {
              position.current = media.current?.currentTime ?? 0
            }}
            onLoadedMetadata={() => {
              if (media.current) media.current.currentTime = position.current
            }}
            onError={() => setPreview('')}
          />
        ))}
      {link.isError && (
        <p role='alert' className='text-destructive text-xs'>
          {t('Unable to authorize this attachment')}
        </p>
      )}
    </div>
  )
}
