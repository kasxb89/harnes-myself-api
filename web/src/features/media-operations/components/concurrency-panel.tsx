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
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { canAccessAdminRoute } from '@/lib/admin-permissions'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { getServerErrorMessage } from '@/lib/server-error-message'
import { useAuthStore, type AuthUser } from '@/stores/auth-store'

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

type AuthorizedAction = {
  action: PendingAction
  generation: number
}

function canEditMediaConcurrency(
  user: AuthUser | null,
  userId: number
): boolean {
  return Boolean(
    user &&
    user.id === userId &&
    user.role >= ROLE.SUPER_ADMIN &&
    canAccessAdminRoute(user, '/media-operations')
  )
}

export function MediaConcurrencyPanel(props: {
  active: boolean
  kind?: 'image' | 'video'
  userId: number
}) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const canEdit = canEditMediaConcurrency(user, props.userId)
  const authorization = useRef({ mounted: false, generation: 0 })
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
    queryFn: ({ signal }) => getMediaConcurrency(signal),
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
  const acceptsMutationResult = (operation: AuthorizedAction): boolean => {
    const currentUser = useAuthStore.getState().auth.user
    return (
      authorization.current.mounted &&
      authorization.current.generation === operation.generation &&
      canEditMediaConcurrency(currentUser, props.userId)
    )
  }
  const save = useMutation({
    mutationFn: (operation: AuthorizedAction) =>
      operation.action.kind === 'save'
        ? saveMediaConcurrency(operation.action.request)
        : reconcileMediaConcurrency(),
    retry: false,
    meta: { errorToast: false },
    onSuccess: (snapshot, operation) => {
      if (!acceptsMutationResult(operation)) {
        return
      }
      queryClient.setQueryData(queryKey, snapshot)
      setResetEpoch((value) => value + 1)
      setConflict(false)
      setPending(null)
      setNotice(operation.action.kind === 'save' ? 'saved' : 'reconciled')
    },
    onError: (error, operation) => {
      if (!acceptsMutationResult(operation)) {
        return
      }
      setPending(null)
      if (error instanceof MediaConcurrencyConflict) {
        setConflict(true)
        setNotice(null)
      } else {
        setNotice(
          operation.action.kind === 'reconcile' ? 'reconcile-failed' : 'failed'
        )
        handleServerError(
          error,
          operation.action.kind === 'reconcile'
            ? t('Failed to reconcile account pools')
            : t('Failed to save account concurrency')
        )
      }
    },
  })
  const resetMutation = save.reset
  useEffect(() => {
    const scope = authorization.current
    scope.mounted = true
    const unsubscribe = useAuthStore.subscribe((state, previous) => {
      if (
        state.auth.user?.id === previous.auth.user?.id &&
        state.auth.session?.sid === previous.auth.session?.sid &&
        canEditMediaConcurrency(state.auth.user, props.userId) ===
          canEditMediaConcurrency(previous.auth.user, props.userId)
      ) {
        return
      }
      scope.generation += 1
      resetMutation()
      setPending(null)
      setNotice(null)
      setConflict(false)
      setResetEpoch((value) => value + 1)
    })
    return () => {
      scope.mounted = false
      scope.generation += 1
      unsubscribe()
    }
  }, [props.userId, resetMutation])
  const loadError = query.isError
    ? getServerErrorMessage(
        query.error,
        t('Failed to load account concurrency')
      )
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
      aria-label={t('Account concurrency configuration')}
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
        <LoadingState inline message={t('Loading account concurrency')} />
      )}
      {query.isError && (
        <ErrorState
          title={t('Failed to load account concurrency')}
          description={
            loadError === t('Failed to load account concurrency')
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
              'Account concurrency changed elsewhere. Reload the latest configuration before saving. Reloading discards this draft.'
            )}
          </AlertDescription>
        </Alert>
      )}
      {notice === 'failed' && (
        <Alert variant='destructive'>
          <AlertDescription>
            {t('Failed to save account concurrency')}
          </AlertDescription>
        </Alert>
      )}
      {notice === 'reconcile-failed' && (
        <Alert variant='destructive'>
          <AlertDescription>
            {t('Failed to reconcile account pools')}
          </AlertDescription>
        </Alert>
      )}
      {notice === 'saved' && (
        <p role='status'>{t('Account concurrency saved')}</p>
      )}
      {notice === 'reconciled' && (
        <p role='status'>{t('Account pools reconciled')}</p>
      )}
      {query.data && !query.isError && (
        <MediaConcurrencyForm
          key={`${query.data.revision}:${resetEpoch}`}
          snapshot={query.data}
          kind={props.kind}
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
            : t('Save account concurrency')
        }
        desc={
          pending?.kind === 'reconcile'
            ? t(
                'Register configured image and video keys and reconcile recorded occupancy. This does not call providers or generate images or videos. Unsaved changes will be discarded.'
              )
            : t(
                'Apply these per-key admission limits? Running and UNKNOWN tasks are preserved; a lower limit only blocks new submissions.'
              )
        }
        confirmText={t('Confirm')}
        isLoading={save.isPending}
        handleConfirm={() => {
          if (pending && canEdit && !save.isPending) {
            save.mutate({
              action: pending,
              generation: authorization.current.generation,
            })
          }
        }}
      />
    </section>
  )
}
