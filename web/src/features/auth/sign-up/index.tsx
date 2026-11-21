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
import { useQuery } from '@tanstack/react-query'
import { Link } from '@tanstack/react-router'
import { lazy, Suspense, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { normalizeRelayRoot } from '@/features/relay-portal/guide-content'
import { RelayPortal } from '@/features/relay-portal/portal'
import { useSystemConfig } from '@/hooks/use-system-config'
import { statusQueryOptions } from '@/lib/status-query'

import { TermsFooter } from '../components/terms-footer'
import type { SystemStatus } from '../types'
import { SignUpForm } from './components/sign-up-form'

const RelayAccessGuide = lazy(() =>
  import('@/features/relay-portal/guide').then((module) => ({
    default: module.RelayAccessGuide,
  }))
)

export function SignUp() {
  const { t } = useTranslation()
  const { systemName, logo } = useSystemConfig()
  const query = useQuery(statusQueryOptions)
  const status = query.data as SystemStatus | null | undefined
  const [guideOpen, setGuideOpen] = useState(false)
  const registerEnabled =
    status?.register_enabled ?? status?.data?.register_enabled
  const passwordEnabled =
    status?.password_register_enabled ?? status?.data?.password_register_enabled

  let form
  if (query.isPending) {
    form = <LoadingState />
  } else if (
    !status ||
    typeof registerEnabled !== 'boolean' ||
    typeof passwordEnabled !== 'boolean'
  ) {
    form = (
      <ErrorState
        title={t('Registration status unavailable')}
        onRetry={() => void query.refetch()}
      />
    )
  } else if (!registerEnabled || !passwordEnabled) {
    form = (
      <div className='space-y-4' role='status'>
        <p>
          {t(
            registerEnabled
              ? 'Password registration is currently closed. Use an available sign-in method.'
              : 'New account registration is currently closed. Existing accounts can still sign in.'
          )}
        </p>
        <Link
          to='/sign-in'
          className='font-medium underline underline-offset-4'
        >
          {t('Sign in')}
        </Link>
      </div>
    )
  } else {
    form = <SignUpForm />
  }

  return (
    <>
      <RelayPortal
        systemName={systemName}
        logo={logo}
        onReadGuide={() => setGuideOpen(true)}
      >
        {form}
        <TermsFooter
          variant='sign-up'
          status={status ?? null}
          className='text-center'
        />
      </RelayPortal>
      <Dialog
        open={guideOpen}
        onOpenChange={setGuideOpen}
        title={t('Relay access guide')}
        contentClassName='relay-obsidian-guide sm:max-w-6xl'
        contentHeight='min(74svh, 850px)'
      >
        {guideOpen && (
          <Suspense fallback={<LoadingState />}>
            <RelayAccessGuide
              rootURL={normalizeRelayRoot(query.data?.server_address)}
            />
          </Suspense>
        )}
      </Dialog>
    </>
  )
}
