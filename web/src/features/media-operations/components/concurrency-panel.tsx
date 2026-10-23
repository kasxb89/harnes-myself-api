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

import { ConfirmDialog } from '@/components/confirm-dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { canAccessAdminRoute } from '@/lib/admin-permissions'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { getServerErrorMessage } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import {
  getMediaConcurrency,
  MediaConcurrencyConflict,
  reconcileMediaConcurrency,
  saveMediaConcurrency,
} from '../concurrency-api'
import type { MediaConcurrencyUpdate } from '../concurrency-schema'
import { MediaConcurrencyForm } from './concurrency-form'

type PendingAction =
  | { kind: 'save'; request: MediaConcurrencyUpdate }
  | { kind: 'reconcile' }

export function MediaConcurrencyPanel(props: {
  active: boolean
  userId: number
}) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const canEdit = Boolean(
    user && user.id === props.userId && user.role >= ROLE.SUPER_ADMIN
  )
  const queryClient = useQueryClient()
  const queryKey = ['media-concurrency', props.userId]
  const [conflict, setConflict] = useState(false)
  const [pending, setPending] = useState<PendingAction | null>(null)
  const [notice, setNotice] = useState<
    'saved' | 'reconciled' | 'failed' | 'reconcile-failed' | null
  >(null)
  const [resetEpoch, setResetEpoch] = useState(0)
  const query = useQuery({
    queryKey,
    queryFn: getMediaConcurrency,
    enabled:
      props.active &&
      Boolean(
        user &&
        user.id === props.userId &&
        canAccessAdminRoute(user, '/media-operations')
      ),
    retry: false,
    staleTime: Infinity,
    refetchOnWindowFocus: false,
    meta: { errorToast: false },
  })
  const save = useMutation({
    mutationFn: (action: PendingAction) =>
      action.kind === 'save'
        ? saveMediaConcurrency(action.request)
        : reconcileMediaConcurrency(),
    retry: false,
    meta: { errorToast: false },
    onSuccess: (snapshot, action) => {
      queryClient.setQueryData(queryKey, snapshot)
      setResetEpoch((value) => value + 1)
      setConflict(false)
      setPending(null)
      setNotice(action.kind === 'save' ? 'saved' : 'reconciled')
    },
    onError: (error, action) => {
      setPending(null)
      if (error instanceof MediaConcurrencyConflict) {
        setConflict(true)
        setNotice(null)
      } else {
        setNotice(action.kind === 'reconcile' ? 'reconcile-failed' : 'failed')
        handleServerError(
          error,
          action.kind === 'reconcile'
            ? t('Failed to reconcile media account pools')
            : t('Failed to save media concurrency')
        )
      }
    },
  })
  const loadError = query.isError
    ? getServerErrorMessage(query.error, t('Failed to load media concurrency'))
    : undefined
  if (
    !user ||
    user.id !== props.userId ||
    !canAccessAdminRoute(user, '/media-operations')
  ) {
    return <ErrorState title={t('Access denied')} />
  }
  const reload = async () => {
    const result = await query.refetch()
    if (result.isSuccess) {
      setConflict(false)
      setNotice(null)
      setResetEpoch((value) => value + 1)
    }
  }
  return (
    <section
      aria-label={t('Media concurrency configuration')}
      className='min-w-0 space-y-4'
    >
      <div className='flex flex-wrap gap-2'>
        <Button
          variant='outline'
          disabled={query.isFetching || save.isPending}
          onClick={() => {
            void reload()
          }}
        >
          {t('Reload configuration')}
        </Button>
        {canEdit && (
          <Button
            variant='outline'
            disabled={query.isFetching || save.isPending || conflict}
            onClick={() => setPending({ kind: 'reconcile' })}
          >
            {t('Reconcile account pools')}
          </Button>
        )}
      </div>
      {query.isFetching && (
        <LoadingState inline message={t('Loading media concurrency')} />
      )}
      {query.isError && (
        <ErrorState
          title={t('Failed to load media concurrency')}
          description={
            loadError === t('Failed to load media concurrency')
              ? undefined
              : loadError
          }
          onRetry={() => {
            void reload()
          }}
        />
      )}
      {conflict && (
        <Alert variant='destructive'>
          <AlertDescription>
            {t(
              'Media concurrency changed elsewhere. Reload the latest configuration before saving. Reloading discards this draft.'
            )}
          </AlertDescription>
        </Alert>
      )}
      {notice === 'failed' && (
        <Alert variant='destructive'>
          <AlertDescription>
            {t('Failed to save media concurrency')}
          </AlertDescription>
        </Alert>
      )}
      {notice === 'reconcile-failed' && (
        <Alert variant='destructive'>
          <AlertDescription>
            {t('Failed to reconcile media account pools')}
          </AlertDescription>
        </Alert>
      )}
      {notice === 'saved' && (
        <p role='status'>{t('Media concurrency saved')}</p>
      )}
      {notice === 'reconciled' && (
        <p role='status'>{t('Media account pools reconciled')}</p>
      )}
      {query.data && !query.isError && (
        <MediaConcurrencyForm
          key={`${query.data.revision}:${resetEpoch}`}
          snapshot={query.data}
          canEdit={canEdit}
          busy={query.isFetching || save.isPending}
          conflict={conflict}
          onSave={(request) => {
            if (canEdit) setPending({ kind: 'save', request })
          }}
        />
      )}
      <ConfirmDialog
        open={pending !== null && canEdit}
        onOpenChange={(open) => {
          if (!open && !save.isPending) setPending(null)
        }}
        title={
          pending?.kind === 'reconcile'
            ? t('Reconcile account pools')
            : t('Save media concurrency')
        }
        desc={
          pending?.kind === 'reconcile'
            ? t(
                'Register configured media keys and reconcile recorded occupancy. This does not call providers or generate media. Unsaved changes will be discarded.'
              )
            : t(
                'Apply these per-key admission limits? Running and UNKNOWN tasks are preserved; a lower limit only blocks new submissions.'
              )
        }
        confirmText={t('Confirm')}
        isLoading={save.isPending}
        handleConfirm={() => {
          if (pending && canEdit && !save.isPending) save.mutate(pending)
        }}
      />
    </section>
  )
}
