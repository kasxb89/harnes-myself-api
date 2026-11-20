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
import { zodResolver } from '@hookform/resolvers/zod'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'

import { StaticDataTable } from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { NativeSelect } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatTimestampToDate } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { getServerErrorSources } from '@/lib/server-error-message'

import {
  deliveryStatuses,
  deliveryStatusLabels,
  emailDetailURL,
  emailTestActionSchema,
  emailTypes,
  emailTypeLabels,
  loadEmailConfig,
  loadEmailDeliveries,
  loadEmailTestReceipt,
  normalizeRecipients,
  saveEmailConfig,
  sendTestEmail,
  validAdminBaseURL,
  type EmailConfigReply,
  type EmailDelivery,
  type EmailTestAction,
} from './email-api'

export function DemandEmailNotifications({
  accountId,
  canWrite,
}: {
  accountId: number
  canWrite: boolean
}) {
  const { t } = useTranslation()
  const query = useQuery({
    queryKey: ['demand-email-config', accountId],
    queryFn: ({ signal }) => loadEmailConfig(signal),
    retry: false,
  })
  if (query.isPending) return <LoadingState />
  if (!query.data) {
    return (
      <ErrorState
        title={t('Email settings unavailable')}
        onRetry={() => void query.refetch()}
      />
    )
  }
  return (
    <div className='space-y-8 pb-6'>
      <EmailSettings
        key={`${accountId}:${query.data.config.revision}`}
        accountId={accountId}
        canWrite={canWrite}
        initial={query.data}
      />
      <EmailDeliveries accountId={accountId} />
    </div>
  )
}

function EmailSettings({
  accountId,
  canWrite,
  initial,
}: {
  accountId: number
  canWrite: boolean
  initial: EmailConfigReply
}) {
  const { t } = useTranslation()
  const cache = useQueryClient()
  const draftSchema = z
    .object({
      revision: z.number().int(),
      enabled: z.boolean(),
      recipientsText: z.string(),
      types: z.object({
        official_created: z.boolean(),
        community_pending: z.boolean(),
        customer_message: z.boolean(),
      }),
      message_merge_seconds: z
        .number({ error: t('Merge window must be 30 to 3600 seconds') })
        .int(t('Merge window must be 30 to 3600 seconds'))
        .min(30, t('Merge window must be 30 to 3600 seconds'))
        .max(3600, t('Merge window must be 30 to 3600 seconds')),
      admin_base_url: z.string().trim().max(2048),
    })
    .superRefine((value, ctx) => {
      const recipients = normalizeRecipients(value.recipientsText)
      if (
        recipients.length > 20 ||
        recipients.some((v) => !z.email().max(254).safeParse(v).success)
      ) {
        ctx.addIssue({
          code: 'custom',
          path: ['recipientsText'],
          message: t('Enter up to 20 valid email addresses'),
        })
      }
      if (value.enabled && recipients.length === 0) {
        ctx.addIssue({
          code: 'custom',
          path: ['recipientsText'],
          message: t('Add at least one recipient before enabling'),
        })
      }
      if (
        (value.enabled || value.admin_base_url) &&
        !validAdminBaseURL(value.admin_base_url)
      ) {
        ctx.addIssue({
          code: 'custom',
          path: ['admin_base_url'],
          message: t(
            'Use an HTTPS console URL without credentials, query or fragment'
          ),
        })
      }
    })
  type Draft = z.infer<typeof draftSchema>
  const defaults = {
    ...initial.config,
    recipientsText: initial.config.recipients.join('\n'),
  }
  const form = useForm<Draft>({
    defaultValues: defaults,
    resolver: zodResolver(draftSchema),
  })
  const actionKey = `demand-email-test:${accountId}`
  const [pending, setPending] = useState<EmailTestAction | null>(() => {
    try {
      const value = sessionStorage.getItem(actionKey)
      return value ? emailTestActionSchema.parse(JSON.parse(value)) : null
    } catch {
      return null
    }
  })
  const actionLock = useRef(false)
  const [notice, setNotice] = useState('')
  const save = useMutation({
    mutationFn: saveEmailConfig,
    retry: false,
    onSuccess: (value) => {
      form.reset({
        ...value.config,
        recipientsText: value.config.recipients.join('\n'),
      })
      cache.setQueryData(['demand-email-config', accountId], value)
      setNotice('Email settings saved')
    },
    onError: (error) =>
      handleServerError(error, undefined, {
        title: t('Email settings could not be saved. Your changes are kept.'),
      }),
  })
  const reload = useMutation({
    mutationFn: () => loadEmailConfig(),
    retry: false,
    onSuccess: (value) => {
      form.reset({
        ...value.config,
        recipientsText: value.config.recipients.join('\n'),
      })
      cache.setQueryData(['demand-email-config', accountId], value)
    },
    onError: (error) =>
      handleServerError(error, undefined, {
        title: t('Email settings unavailable'),
      }),
  })
  const received = () => {
    try {
      sessionStorage.removeItem(actionKey)
    } catch {
      /* A retained receipt is safe to query again. */
    }
    setPending(null)
    actionLock.current = false
    setNotice(
      'Test email queued. Check the sending records for each recipient.'
    )
    void cache.invalidateQueries({
      queryKey: ['demand-email-deliveries', accountId],
    })
  }
  const test = useMutation({
    mutationFn: sendTestEmail,
    retry: false,
    onSuccess: received,
    onError: (error) => {
      actionLock.current = false
      const rejected = getServerErrorSources(error).some((source) =>
        [
          'DEMAND_EMAIL_INVALID_INPUT',
          'DEMAND_EMAIL_REVISION_CONFLICT',
          'DEMAND_EMAIL_SMTP_UNAVAILABLE',
        ].includes(String(source.code))
      )
      if (rejected) {
        try {
          sessionStorage.removeItem(actionKey)
        } catch {
          /* No server event was committed. */
        }
        setPending(null)
        setNotice(
          'The test was not queued. Reload saved settings before trying again.'
        )
        handleServerError(error, undefined, {
          title: t(
            'The test was not queued. Reload saved settings before trying again.'
          ),
        })
      } else {
        handleServerError(error, undefined, {
          title: t(
            'Test result is not confirmed. Check the original receipt before sending again.'
          ),
        })
      }
    },
  })
  const check = useMutation({
    mutationFn: loadEmailTestReceipt,
    retry: false,
    onSuccess: received,
    onError: (error) =>
      handleServerError(error, undefined, {
        title: t(
          'Original receipt is not available yet. No new email was requested.'
        ),
      }),
  })
  const startTest = () => {
    if (pending || actionLock.current) return
    const action = {
      client_action_id: crypto.randomUUID(),
      revision: initial.config.revision,
    }
    try {
      sessionStorage.setItem(actionKey, JSON.stringify(action))
    } catch {
      setNotice(
        'The test request could not be saved locally. No email was requested.'
      )
      return
    }
    actionLock.current = true
    setPending(action)
    setNotice('')
    test.mutate(action)
  }
  const testReady =
    canWrite &&
    initial.smtp_ready &&
    !form.formState.isDirty &&
    initial.config.recipients.length > 0 &&
    validAdminBaseURL(initial.config.admin_base_url)
  return (
    <section aria-label={t('Demand email notifications')} className='space-y-5'>
      <div className='flex flex-wrap items-start justify-between gap-3'>
        <div>
          <h2 className='text-base font-semibold'>
            {t('Demand email notifications')}
          </h2>
          <p className='text-muted-foreground mt-1 text-sm'>
            {t(
              'Notify your team after a demand or customer message has been saved.'
            )}
          </p>
        </div>
        <Badge variant={initial.smtp_ready ? 'secondary' : 'outline'}>
          {t(initial.smtp_ready ? 'SMTP configured' : 'SMTP not configured')}
        </Badge>
      </div>
      <Form {...form}>
        <form
          className='max-w-3xl space-y-5'
          onSubmit={form.handleSubmit(({ recipientsText, ...value }) =>
            save.mutate({
              ...value,
              recipients: normalizeRecipients(recipientsText),
            })
          )}
        >
          <fieldset
            disabled={!canWrite || save.isPending || Boolean(pending)}
            className='space-y-5'
          >
            <FormField
              control={form.control}
              name='enabled'
              render={({ field }) => (
                <FormItem className='flex items-center gap-3'>
                  <FormControl>
                    <Checkbox
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                  <FormLabel>
                    {t('Enable demand email notifications')}
                  </FormLabel>
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='recipientsText'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Notification recipients')}</FormLabel>
                  <FormControl>
                    <Textarea
                      {...field}
                      rows={3}
                      maxLength={5200}
                      placeholder={t('One email address per line')}
                    />
                  </FormControl>
                  <p className='text-muted-foreground text-xs'>
                    {t('Up to 20 recipients. Each receives a separate email.')}
                  </p>
                  <FormMessage />
                </FormItem>
              )}
            />
            <div className='space-y-3'>
              <h3 className='text-sm font-medium'>{t('Notification types')}</h3>
              {(
                [
                  'official_created',
                  'community_pending',
                  'customer_message',
                ] as const
              ).map((type) => (
                <FormField
                  key={type}
                  control={form.control}
                  name={`types.${type}`}
                  render={({ field }) => (
                    <FormItem className='flex items-center gap-3'>
                      <FormControl>
                        <Checkbox
                          checked={field.value}
                          onCheckedChange={field.onChange}
                        />
                      </FormControl>
                      <FormLabel>{t(emailTypeLabels[type])}</FormLabel>
                    </FormItem>
                  )}
                />
              ))}
            </div>
            <FormField
              control={form.control}
              name='message_merge_seconds'
              render={({ field }) => (
                <FormItem className='max-w-xs'>
                  <FormLabel>
                    {t('Customer message merge window (seconds)')}
                  </FormLabel>
                  <FormControl>
                    <Input
                      {...field}
                      type='number'
                      min={30}
                      max={3600}
                      onChange={(e) =>
                        field.onChange(
                          e.target.value === ''
                            ? Number.NaN
                            : Number(e.target.value)
                        )
                      }
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
            <p className='text-muted-foreground text-xs'>
              {t(
                'New orders notify immediately. Customer messages are merged within the first message window; administrator replies are excluded.'
              )}
            </p>
            <FormField
              control={form.control}
              name='admin_base_url'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Management console URL')}</FormLabel>
                  <FormControl>
                    <Input
                      {...field}
                      type='url'
                      maxLength={2048}
                      placeholder='https://console.example.com'
                    />
                  </FormControl>
                  <p className='text-muted-foreground text-xs'>
                    {t(
                      'Use the console address that recipients can open. A deployment path prefix is supported.'
                    )}
                  </p>
                  <FormMessage />
                </FormItem>
              )}
            />
            <p className='text-muted-foreground text-xs'>
              {t(
                'Disabling stops new reminders and pauses queued mail. Paused history is not sent again when re-enabled.'
              )}
            </p>
            {canWrite && (
              <Button type='submit' disabled={save.isPending}>
                {t(save.isPending ? 'Saving...' : 'Save settings')}
              </Button>
            )}
          </fieldset>
          {save.isError && (
            <div role='alert' className='space-y-2 text-sm'>
              <p>
                {t('Email settings could not be saved. Your changes are kept.')}
              </p>
              <Button
                type='button'
                variant='outline'
                disabled={reload.isPending}
                onClick={() => reload.mutate()}
              >
                {t('Reload saved settings')}
              </Button>
            </div>
          )}
        </form>
      </Form>
      <div className='max-w-3xl space-y-3 border-t pt-4'>
        <h3 className='text-sm font-medium'>{t('Test email')}</h3>
        <p className='text-muted-foreground text-xs'>
          {t(
            'A test sends one email to each saved recipient, even while notifications are off. Save changes before testing.'
          )}
        </p>
        {canWrite && (
          <div className='flex flex-wrap gap-2'>
            {pending ? (
              <Button
                type='button'
                variant='outline'
                disabled={test.isPending || check.isPending}
                onClick={() => check.mutate(pending)}
              >
                {t('Check original test receipt')}
              </Button>
            ) : (
              <Button
                type='button'
                variant='outline'
                disabled={!testReady || save.isPending}
                onClick={startTest}
              >
                {t('Send one test email per recipient')}
              </Button>
            )}
          </div>
        )}
        {pending && (
          <p role='status' className='text-muted-foreground text-sm'>
            {t(
              'A test request is pending confirmation. Its original identifier is retained; no new request will be sent.'
            )}
          </p>
        )}
        {test.isError && !pending && (
          <Button
            type='button'
            variant='outline'
            disabled={reload.isPending}
            onClick={() => reload.mutate()}
          >
            {t('Reload saved settings')}
          </Button>
        )}
        {check.isError && (
          <p role='alert' className='text-sm'>
            {t(
              'Original receipt is not available yet. No new email was requested.'
            )}
          </p>
        )}
        {notice && (
          <p role='status' className='text-sm'>
            {t(notice)}
          </p>
        )}
      </div>
    </section>
  )
}

function deliveryExplanation(row: EmailDelivery) {
  if (row.status === 'unknown') {
    return 'The server could not confirm SMTP acceptance. Automatic resend is stopped.'
  }
  if (row.status === 'sent') {
    return 'Accepted by SMTP; inbox delivery is not guaranteed.'
  }
  if (row.status === 'paused') {
    const reasons: Record<string, string> = {
      disabled: 'Notifications are disabled.',
      type_disabled: 'This notification type is disabled.',
      recipient_removed: 'The recipient was removed.',
      already_reviewed: 'The demand no longer requires review.',
      entity_unavailable: 'The demand is no longer available.',
    }
    return (
      reasons[row.pause_reason] ??
      'Paused by notification settings or recipient changes.'
    )
  }
  if (row.status === 'failed') {
    if (row.error_code === 'smtp_not_configured') {
      return 'SMTP is not configured'
    }
    if (row.error_code === 'smtp_invalid_message') {
      return 'The email format is invalid.'
    }
    if (
      row.error_code === 'smtp_tls_unavailable' ||
      /^smtp_tls_(failed|rejected)$/.test(row.error_code)
    ) {
      return 'The secure SMTP connection failed.'
    }
    if (row.error_code === 'smtp_timeout') return 'The SMTP request timed out.'
    if (/^smtp_auth_(failed|rejected)$/.test(row.error_code)) {
      return 'SMTP authentication failed.'
    }
    if (/^smtp_recipient_(failed|rejected)$/.test(row.error_code)) {
      return 'SMTP rejected the recipient.'
    }
    if (row.error_code === 'smtp_data_rejected') {
      return 'SMTP rejected the email content.'
    }
    return 'The email could not be submitted to SMTP.'
  }
  if (row.status === 'leased' || row.status === 'sending') {
    return 'The mail worker is processing this delivery.'
  }
  return 'Waiting for the send time or bounded retry.'
}
function EmailDeliveries({ accountId }: { accountId: number }) {
  const { t, i18n } = useTranslation()
  const [filters, setFilters] = useState({ status: '', type: '', cursor: '' })
  const [history, setHistory] = useState<string[]>([])
  const query = useQuery({
    queryKey: ['demand-email-deliveries', accountId, filters],
    queryFn: ({ signal }) => loadEmailDeliveries(filters, signal),
    retry: false,
    refetchInterval: 10000,
  })
  const change = (name: 'status' | 'type', value: string) => {
    setFilters((old) => ({ ...old, [name]: value, cursor: '' }))
    setHistory([])
  }
  return (
    <section
      aria-label={t('Email sending records')}
      className='space-y-3 border-t pt-6'
    >
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <h2 className='text-base font-semibold'>
          {t('Email sending records')}
        </h2>
        <Button
          variant='ghost'
          size='sm'
          disabled={query.isFetching}
          onClick={() => void query.refetch()}
        >
          {t('Refresh')}
        </Button>
      </div>
      <div className='flex flex-wrap gap-2'>
        <NativeSelect
          className='w-44'
          aria-label={t('Email delivery status')}
          value={filters.status}
          onChange={(e) => change('status', e.target.value)}
        >
          <option value=''>{t('All statuses')}</option>
          {deliveryStatuses.map((status) => (
            <option value={status} key={status}>
              {t(deliveryStatusLabels[status])}
            </option>
          ))}
        </NativeSelect>
        <NativeSelect
          className='w-60'
          aria-label={t('Notification type')}
          value={filters.type}
          onChange={(e) => change('type', e.target.value)}
        >
          <option value=''>{t('All notification types')}</option>
          {emailTypes.map((type) => (
            <option value={type} key={type}>
              {t(emailTypeLabels[type])}
            </option>
          ))}
        </NativeSelect>
      </div>
      {query.isPending && <LoadingState />}
      {query.isError && (
        <ErrorState
          title={t('Sending records unavailable')}
          onRetry={() => void query.refetch()}
        />
      )}
      {query.data && (
        <StaticDataTable
          data={query.data.items}
          getRowKey={(row) => row.id}
          emptyContent={<EmptyState title={t('No email records yet')} />}
          columns={[
            {
              id: 'recipient',
              header: t('Notification recipient'),
              cell: (row) => <span className='break-all'>{row.recipient}</span>,
            },
            {
              id: 'event',
              header: t('Notification type'),
              cell: (row) => (
                <div className='max-w-72 space-y-1 whitespace-normal'>
                  <p>{t(emailTypeLabels[row.type])}</p>
                  <p className='text-muted-foreground text-xs'>
                    {row.title || row.number || t('Test email')}
                  </p>
                  <p className='text-muted-foreground text-xs'>
                    {t('Merged events: {{value}}', {
                      value: formatNumber(
                        row.event_count,
                        toIntlLocale(i18n.language)
                      ),
                    })}
                  </p>
                </div>
              ),
            },
            {
              id: 'status',
              header: t('Status'),
              cell: (row) => (
                <div className='max-w-64 space-y-1 whitespace-normal'>
                  <Badge
                    variant={row.status === 'sent' ? 'secondary' : 'outline'}
                  >
                    {t(deliveryStatusLabels[row.status])}
                  </Badge>
                  <p className='text-muted-foreground text-xs'>
                    {t(deliveryExplanation(row))}
                  </p>
                </div>
              ),
            },
            {
              id: 'created',
              header: t('Created at'),
              cell: (row) => (
                <time className='whitespace-nowrap'>
                  {formatTimestampToDate(row.created_at)}
                </time>
              ),
            },
            {
              id: 'detail',
              header: t('Details'),
              cell: (row) => {
                const url = emailDetailURL(row)
                return url ? (
                  <Link
                    className='text-primary underline underline-offset-4'
                    to='/demand-management'
                    search={
                      row.domain === 'official'
                        ? { tab: 'demands', demandId: row.entity_id }
                        : { tab: 'community', orderId: row.entity_id }
                    }
                  >
                    {t('Open demand')}
                  </Link>
                ) : (
                  <span className='text-muted-foreground'>—</span>
                )
              },
            },
          ]}
        />
      )}
      <div className='flex justify-end gap-2'>
        <Button
          variant='ghost'
          size='sm'
          disabled={history.length === 0 || query.isFetching}
          onClick={() => {
            setFilters((v) => ({ ...v, cursor: history.at(-1) ?? '' }))
            setHistory((v) => v.slice(0, -1))
          }}
        >
          {t('Previous')}
        </Button>
        <Button
          variant='ghost'
          size='sm'
          disabled={!query.data?.has_more || query.isFetching}
          onClick={() => {
            setHistory((v) => [...v, filters.cursor])
            setFilters((v) => ({ ...v, cursor: query.data?.next_cursor ?? '' }))
          }}
        >
          {t('Next')}
        </Button>
      </div>
    </section>
  )
}
