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
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { getMediaOperations, MediaQueryTooBroadError } from './api'
import { MediaConcurrencyPanel } from './components/concurrency-panel'
import { MediaCurrentTable } from './components/current-table'
import { MediaErrorBreakdown } from './components/error-breakdown'
import { MediaOperationsFilters } from './components/filters'
import { MediaHistorySummary } from './components/history-summary'
import { MediaHistoryTable } from './components/history-table'
import { MediaPollTable } from './components/poll-table'
import { MediaOperationsSnapshot } from './components/snapshot'
import { initialMediaFilters, mediaQueryFromDraft } from './lib/filters'
import { useMediaFormat } from './lib/format'

export function MediaOperationsPage() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  if (!user || user.role < ROLE.ADMIN) {
    return <ErrorState title={t('Access denied')} />
  }
  return <MediaOperationsReport key={user.id} userId={user.id} />
}

function MediaOperationsReport(props: { userId: number }) {
  const { t } = useTranslation()
  const format = useMediaFormat()
  const [section, setSection] = useState('observations')
  const [observedModels, setObservedModels] = useState<string[]>([])
  const [draft, setDraft] = useState(initialMediaFilters)
  const [filters, setFilters] = useState(() => {
    const initial = mediaQueryFromDraft(draft)
    if (!initial) throw new Error('Invalid initial media operations period')
    return initial
  })
  const parsed = mediaQueryFromDraft(draft)
  const reports = useQuery({
    queryKey: ['media-operations', props.userId, filters],
    queryFn: () => getMediaOperations(filters),
    retry: false,
    meta: { errorToast: false },
  })
  const refresh = () => {
    void reports.refetch()
  }
  const data = reports.data
  useEffect(() => {
    if (!data) return
    setObservedModels((previous) => {
      const next = [
        ...new Set(
          [...previous, ...data.models.map((row) => row.model)].filter(Boolean)
        ),
      ].sort()
      if (
        next.length === previous.length &&
        next.every((model, index) => model === previous[index])
      ) {
        return previous
      }
      return next
    })
  }, [data])
  const tooBroad = reports.error instanceof MediaQueryTooBroadError
  const models = [
    ...new Set(
      [
        ...observedModels,
        ...(data?.models.map((row) => row.model) ?? []),
        draft.model,
      ].filter(Boolean)
    ),
  ].sort()
  return (
    <SectionPageLayout stackActionsOnMobile>
      <SectionPageLayout.Title>
        {t('Image and video operations')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        {section === 'observations' && (
          <Button
            variant='outline'
            disabled={reports.isFetching}
            onClick={refresh}
          >
            {t('Refresh')}
          </Button>
        )}
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <Tabs
          value={section}
          onValueChange={(value) => setSection(String(value))}
          className='min-w-0 gap-4'
        >
          <TabsList aria-label={t('Media operation sections')}>
            <TabsTrigger value='observations'>
              {t('Operational observations')}
            </TabsTrigger>
            <TabsTrigger value='concurrency'>
              {t('Concurrency settings')}
            </TabsTrigger>
          </TabsList>
          <TabsContent value='observations' keepMounted>
            <div className='min-w-0 space-y-4'>
              <Alert>
                <AlertDescription>
                  {t(
                    'Recorded media tasks and errors. Upstream account capacity is unknown.'
                  )}
                </AlertDescription>
              </Alert>
              <MediaOperationsFilters
                value={draft}
                models={models}
                loading={reports.isFetching}
                invalid={!parsed}
                onChange={setDraft}
                onApply={() => {
                  if (parsed) setFilters(parsed)
                }}
                onReset={() => {
                  const next = initialMediaFilters()
                  setDraft(next)
                  const query = mediaQueryFromDraft(next)
                  if (query) setFilters(query)
                }}
              />
              {!parsed && (
                <Alert variant='destructive'>
                  <AlertDescription>
                    {t('Choose a valid past time range of up to 31 days.')}
                  </AlertDescription>
                </Alert>
              )}
              {reports.isFetching && (
                <div role='status' aria-label={t('Loading media operations')}>
                  <LoadingState
                    inline
                    message={t('Loading media operations')}
                  />
                </div>
              )}
              {reports.isError && (
                <ErrorState
                  title={t('Failed to load media operations')}
                  description={
                    tooBroad
                      ? t('Narrow the time range or choose a model')
                      : undefined
                  }
                  onRetry={tooBroad ? undefined : refresh}
                />
              )}
              {!reports.isError && data && (
                <>
                  <p className='text-muted-foreground text-xs'>
                    {t(
                      'Snapshot as of {{time}}. The history period does not limit current task states.',
                      { time: format.time(data.as_of) }
                    )}
                  </p>
                  <MediaOperationsSnapshot data={data} />
                  <details className='text-muted-foreground space-y-2 text-sm'>
                    <summary className='cursor-pointer font-medium'>
                      {t('How to read these records')}
                    </summary>
                    <p>
                      {t(
                        'Tasks are recorded after submission. Missing task records, not-started states and unknown states do not prove that an upstream request was never sent. No free slots or utilization are inferred.'
                      )}
                    </p>
                    <p>
                      {t(
                        'This observation set is incomplete. Historical errors may lack a stage, uncorrelated client reports are not distinct API requests, and cross-database reads are not an atomic snapshot.'
                      )}
                    </p>
                  </details>
                  <Tabs defaultValue='current' className='gap-4'>
                    <TabsList
                      aria-label={t('Media operations views')}
                      className='max-w-full flex-wrap justify-start gap-1 group-data-horizontal/tabs:h-auto'
                    >
                      <TabsTrigger
                        value='current'
                        className='min-h-8 whitespace-normal'
                      >
                        {t('Current task records')}
                      </TabsTrigger>
                      <TabsTrigger
                        value='history'
                        className='min-h-8 whitespace-normal'
                      >
                        {t('Historical observations')}
                      </TabsTrigger>
                      <TabsTrigger
                        value='poll'
                        className='min-h-8 whitespace-normal'
                      >
                        {t('Polling diagnostics')}
                      </TabsTrigger>
                    </TabsList>
                    <TabsContent value='current'>
                      <MediaCurrentTable rows={data.models} />
                    </TabsContent>
                    <TabsContent value='history' className='space-y-4'>
                      <p className='text-muted-foreground text-xs'>
                        {t(
                          'History: {{start}} to {{end}}. Dates use this browser’s time zone.',
                          {
                            start: format.time(data.history_start),
                            end: format.time(data.history_end),
                          }
                        )}
                      </p>
                      <MediaHistorySummary data={data.history_summary} />
                      <Alert>
                        <AlertDescription>
                          {t(
                            'Request counts deduplicate nonempty request IDs. Events are log records. Recorded policy attempts are observations, not actual upstream POST counts; client reports are separate evidence.'
                          )}
                        </AlertDescription>
                      </Alert>
                      <p className='text-muted-foreground text-sm'>
                        {t(
                          'Accepted tasks use submission time. A request can appear in several categories or stages; those request counts must not be added together.'
                        )}
                      </p>
                      <MediaHistoryTable rows={data.models} />
                      <MediaErrorBreakdown history={data.history_summary} />
                    </TabsContent>
                    <TabsContent value='poll' className='space-y-4'>
                      <Alert>
                        <AlertDescription>
                          {t(
                            'Polling failures are the current consecutive counters stored on tasks. They are not historical polling totals or generation submissions; a later successful poll may reset them.'
                          )}
                        </AlertDescription>
                      </Alert>
                      <MediaPollTable rows={data.models} />
                    </TabsContent>
                  </Tabs>
                </>
              )}
            </div>
          </TabsContent>
          <TabsContent value='concurrency' keepMounted>
            <MediaConcurrencyPanel
              active={section === 'concurrency'}
              userId={props.userId}
            />
          </TabsContent>
        </Tabs>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
