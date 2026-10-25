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
import { Pause, Play, RefreshCw } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Separator } from '@/components/ui/separator'
import { formatTimestampToDate } from '@/lib/format'
import { useAuthStore } from '@/stores/auth-store'

import { LoadDetails } from './components/load-details'
import { LoadTrend } from './components/load-trend'
import { ResourceOverview } from './components/resource-overview'
import { canViewServerLoad, serverLoadScope } from './policy'
import { useServerLoad } from './use-server-load'

export function ServerLoadPage() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const sid = useAuthStore((state) => state.auth.session?.sid)
  if (!canViewServerLoad(user)) {
    return <ErrorState title={t('Permission denied')} />
  }
  const scope = serverLoadScope(user, sid)
  return <ServerLoad key={scope} scope={scope} />
}

export function ServerLoad(props: { scope: string }) {
  const { t } = useTranslation()
  const [paused, setPaused] = useState(false)
  const monitor = useServerLoad({ scope: props.scope, paused })
  const query = monitor.query
  const snapshot = monitor.authorized ? query.data : undefined
  let samplingStatus = t('Auto refresh every 10 seconds')
  if (paused) samplingStatus = t('Paused')
  if (!monitor.visible) samplingStatus = t('Page hidden · refresh paused')
  if (!monitor.online) samplingStatus = t('Offline · refresh paused')
  if (!monitor.authorized) return <ErrorState title={t('Permission denied')} />
  return (
    <SectionPageLayout stackActionsOnMobile>
      <SectionPageLayout.Title>{t('Server load')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button
          variant='outline'
          size='sm'
          onClick={() => setPaused((value) => !value)}
          aria-pressed={paused}
        >
          {paused ? <Play aria-hidden='true' /> : <Pause aria-hidden='true' />}
          {paused ? t('Resume') : t('Pause')}
        </Button>
        <Button
          variant='outline'
          size='sm'
          onClick={monitor.refresh}
          disabled={query.isFetching || !monitor.online || !monitor.visible}
        >
          <RefreshCw aria-hidden='true' />
          {t('Refresh')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='flex min-w-0 flex-col'>
          <div
            className='text-muted-foreground flex flex-wrap items-center gap-x-4 gap-y-2 text-xs'
            aria-live='polite'
          >
            {snapshot && (
              <Badge variant='secondary'>{t('API service running')}</Badge>
            )}
            <span>{samplingStatus}</span>
            {snapshot && (
              <>
                <span>
                  {t('Sampled at')}:{' '}
                  {formatTimestampToDate(snapshot.sampled_at)}
                </span>
                <span>
                  {snapshot.host_visibility === 'partial'
                    ? t('Container view · host visibility is partial')
                    : t('System view')}
                </span>
              </>
            )}
          </div>
          {(monitor.stale || (query.isError && snapshot)) && (
            <Alert variant='destructive' className='mt-3'>
              <AlertDescription>
                {query.isError
                  ? t(
                      'Load sampling failed. Showing the last successful sample.'
                    )
                  : t('Sample is stale. These values are not current.')}
              </AlertDescription>
            </Alert>
          )}
          {!snapshot && query.isPending && (
            <LoadingState message={t('Collecting server load...')} />
          )}
          {!snapshot && query.isError && (
            <ErrorState
              title={t('Unable to read server load')}
              onRetry={
                monitor.online && monitor.visible ? monitor.refresh : undefined
              }
            />
          )}
          {snapshot && (
            <>
              <ResourceOverview snapshot={snapshot} />
              <Separator />
              <LoadTrend samples={monitor.samples} snapshot={snapshot} />
              <Separator />
              <LoadDetails snapshot={snapshot} />
            </>
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
