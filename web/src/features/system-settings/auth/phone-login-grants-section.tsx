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
import { getCoreRowModel, useReactTable } from '@tanstack/react-table'
import { useEffect, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { CopyButton } from '@/components/copy-button'
import { DataTablePagination, StaticDataTable } from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  SecureVerificationDialog,
  useSecureVerification,
} from '@/features/auth/secure-verification'
import { api } from '@/lib/api'
import { formatTimestamp } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import {
  AuthOperationError,
  authRequestOptions,
  authResult,
} from '@/lib/secure-verification'

import { SettingsSection } from '../components/settings-section'

type Grant = {
  phone_e164: string
  issued_at: number
  expires_at: number
  used_at: number
  revoked_at: number
  attempts: number
}
type IssuedCode = { grant: Grant; code: string }
const queryKey = ['phone-login-grants']
const schema = z.object({
  phone: z
    .string()
    .trim()
    .regex(
      /^\+[1-9][0-9]{6,14}$/,
      'Enter the full international number including its country code, excluding +86.'
    )
    .refine((value) => !value.startsWith('+86'), {
      message:
        'Enter the full international number including its country code, excluding +86.',
    }),
})

export function PhoneLoginGrantsSection() {
  const { t } = useTranslation()
  const client = useQueryClient()
  const verification = useSecureVerification()
  const [pagination, setPagination] = useState({ pageIndex: 0, pageSize: 20 })
  const [issued, setIssued] = useState<IssuedCode | null>(null)
  const [revokeTarget, setRevokeTarget] = useState<Grant | null>(null)
  const form = useForm<z.infer<typeof schema>>({
    resolver: zodResolver(schema),
    defaultValues: { phone: '' },
  })
  const grants = useQuery({
    queryKey: [...queryKey, pagination],
    queryFn: () =>
      authResult<{ items: Grant[]; total: number }>(
        api.get('/api/option/phone_login_grants', {
          ...authRequestOptions,
          params: {
            p: pagination.pageIndex + 1,
            page_size: pagination.pageSize,
          },
        })
      ),
    refetchInterval: 30_000,
  })
  const table = useReactTable({
    data: grants.data?.items ?? [],
    columns: [],
    getCoreRowModel: getCoreRowModel(),
    manualPagination: true,
    rowCount: grants.data?.total ?? 0,
    state: { pagination },
    onPaginationChange: setPagination,
  })
  useEffect(() => {
    if (!issued) return
    const timeout = window.setTimeout(
      () => setIssued(null),
      Math.max(0, issued.grant.expires_at * 1000 - Date.now())
    )
    return () => window.clearTimeout(timeout)
  }, [issued])
  const manage = useMutation({
    retry: false,
    mutationFn: async (request: {
      phone_e164: string
      action: 'issue' | 'revoke'
    }) => {
      setIssued(null)
      const proof = await verification.requestVerification({
        scope: 'admin.user.phone_grant',
        context: request,
        title: t('Verify manual phone access'),
        description: t(
          'Confirm the phone number {{phone}} belongs to the person you will contact on WeChat.',
          { phone: request.phone_e164 }
        ),
      })
      if (!proof) return
      const data = await authResult<IssuedCode | null>(
        api.post('/api/option/phone_login_grants', request, {
          ...authRequestOptions,
          headers: { 'X-Security-Proof': proof.proof_token },
        })
      )
      if (request.action === 'issue' && data) setIssued(data)
      setRevokeTarget(null)
      setPagination((value) => ({ ...value, pageIndex: 0 }))
      await client.invalidateQueries({ queryKey })
    },
    onError: (error) => handleServerError(AuthOperationError.from(error)),
  })
  const status = (grant: Grant) => {
    if (grant.revoked_at) return t('Revoked')
    if (grant.used_at) return t('Used')
    if (grant.attempts >= 5) return t('Login code locked')
    if (grant.expires_at * 1000 <= Date.now()) return t('Expired')
    return t('Awaiting login')
  }

  return (
    <SettingsSection title={t('Overseas phone whitelist')}>
      <p className='text-muted-foreground max-w-prose text-sm'>
        {t(
          'Approve an exact international phone number, then send its private login code to the user on WeChat. Each code is valid for 10 minutes and one login. Mainland SMS and existing sessions are unchanged.'
        )}
      </p>
      <Form {...form}>
        <form
          className='flex flex-col gap-3 sm:flex-row sm:items-start'
          onSubmit={form.handleSubmit((value) =>
            manage.mutate({ phone_e164: value.phone, action: 'issue' })
          )}
        >
          <FormField
            control={form.control}
            name='phone'
            render={({ field }) => (
              <FormItem className='min-w-0 flex-1'>
                <FormLabel>{t('International phone number')}</FormLabel>
                <FormControl>
                  <Input
                    {...field}
                    type='tel'
                    autoComplete='off'
                    placeholder='+60123456789'
                    disabled={manage.isPending}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <Button className='sm:mt-6' type='submit' disabled={manage.isPending}>
            {t('Generate private login code')}
          </Button>
        </form>
      </Form>
      {issued && (
        <div
          role='status'
          className='bg-muted flex flex-wrap items-center gap-3 rounded-md p-4'
        >
          <div className='min-w-0 flex-1'>
            <p className='font-medium break-all'>{issued.grant.phone_e164}</p>
            <p className='font-mono text-2xl tracking-widest'>{issued.code}</p>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Shown once. Send privately before {{time}}; a new code invalidates the previous one.',
                { time: formatTimestamp(issued.grant.expires_at) }
              )}
            </p>
          </div>
          <CopyButton
            value={issued.code}
            variant='outline'
            size='sm'
            aria-label={t('Copy login code')}
          >
            {t('Copy login code')}
          </CopyButton>
          <Button variant='ghost' onClick={() => setIssued(null)}>
            {t('Dismiss')}
          </Button>
        </div>
      )}
      {grants.isPending && <LoadingState />}
      {grants.isError && <ErrorState onRetry={() => void grants.refetch()} />}
      {grants.data && (
        <>
          <StaticDataTable
            data={grants.data.items}
            getRowKey={(grant) => grant.phone_e164}
            emptyContent={t('No overseas phone numbers approved yet.')}
            columns={[
              {
                id: 'phone',
                header: t('International phone number'),
                cell: (grant) => (
                  <span className='font-mono'>{grant.phone_e164}</span>
                ),
              },
              { id: 'status', header: t('Status'), cell: status },
              {
                id: 'expiry',
                header: t('Expires at'),
                cell: (grant) => formatTimestamp(grant.expires_at),
              },
              {
                id: 'actions',
                header: t('Actions'),
                cell: (grant) => (
                  <div className='flex flex-wrap gap-2'>
                    <Button
                      size='sm'
                      variant='outline'
                      disabled={manage.isPending}
                      onClick={() =>
                        manage.mutate({
                          phone_e164: grant.phone_e164,
                          action: 'issue',
                        })
                      }
                    >
                      {t('New login code')}
                    </Button>
                    <Button
                      size='sm'
                      variant='ghost'
                      disabled={manage.isPending || Boolean(grant.revoked_at)}
                      onClick={() => setRevokeTarget(grant)}
                    >
                      {t('Revoke')}
                    </Button>
                  </div>
                ),
              },
            ]}
          />
          <DataTablePagination table={table} compact />
        </>
      )}
      <ConfirmDialog
        open={Boolean(revokeTarget) && !verification.isActive}
        onOpenChange={(open) => {
          if (!open && !manage.isPending) setRevokeTarget(null)
        }}
        title={t('Revoke manual phone access')}
        desc={t(
          'The current code for {{phone}} will stop working. Existing signed-in sessions are kept.',
          { phone: revokeTarget?.phone_e164 }
        )}
        destructive
        isLoading={manage.isPending}
        handleConfirm={() => {
          if (revokeTarget) {
            manage.mutate({
              phone_e164: revokeTarget.phone_e164,
              action: 'revoke',
            })
          }
        }}
      />
      <SecureVerificationDialog {...verification.dialogProps} />
    </SettingsSection>
  )
}
