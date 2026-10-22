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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { VideoCurrentTable } from './components/current-table'
import { VideoUsageFilters } from './components/filters'
import { VideoHistoryTable } from './components/history-table'
import { VideoUsageMethodology } from './components/methodology'
import { VideoUsageSummary } from './components/summary'
import { VideoTaskTable } from './components/task-table'
import { useVideoFormat } from './lib/format'
import { useVideoReport } from './lib/use-video-report'

export function VideoUsagePage() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  if (!user || user.role < ROLE.ADMIN) {
    return <ErrorState title={t('Access denied')} />
  }
  return <VideoUsageReport key={user.id} userId={user.id} />
}
function VideoUsageReport(props: { userId: number }) {
  const { t } = useTranslation()
  const format = useVideoFormat()
  const [tab, setTab] = useState('current')
  const state = useVideoReport(props.userId)
  const data = state.report.data
  const refresh = () => {
    void state.report.refetch()
  }
  return (
    <SectionPageLayout stackActionsOnMobile>
      <SectionPageLayout.Title>
        {t('Video client usage')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button
          variant='outline'
          onClick={refresh}
          disabled={state.report.isFetching}
        >
          {t('Refresh')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='min-w-0 space-y-4'>
          <Alert>
            <AlertDescription>
              {t(
                'Upstream capacity is unknown. Client online time is not attributed to video models.'
              )}
            </AlertDescription>
          </Alert>
          <VideoUsageFilters
            value={state.draft}
            models={state.models}
            users={state.users}
            invalid={!state.parsed}
            loading={state.report.isFetching}
            onChange={state.setDraft}
            onApply={state.apply}
            onReset={state.reset}
          />
          {!state.parsed && (
            <Alert variant='destructive'>
              <AlertDescription>
                {t('Choose a valid past time range of up to 31 days.')}
              </AlertDescription>
            </Alert>
          )}
          {state.report.isFetching && (
            <div role='status' aria-label={t('Loading video usage')}>
              <LoadingState inline message={t('Loading video usage')} />
            </div>
          )}
          {state.report.isError && (
            <ErrorState
              title={t('Failed to load video usage')}
              description={
                state.tooBroad
                  ? t('Narrow the time range or choose a model')
                  : undefined
              }
              onRetry={state.tooBroad ? undefined : refresh}
            />
          )}
          {!state.report.isError && data && (
            <>
              <p className='text-muted-foreground text-xs'>
                {t('Snapshot: {{time}}', { time: format.time(data.as_of) })}
              </p>
              <VideoUsageSummary summary={data.summary} />
              <p className='text-muted-foreground text-xs'>
                <span>{t('Submitted in the history period')}</span> ·{' '}
                <span>
                  {t(
                    'Source coverage before source filtering: {{desktop}} desktop declarations / {{unknown}} unknown / {{total}} records.',
                    {
                      desktop: format.count(data.source_coverage.desktop),
                      unknown: format.count(data.source_coverage.unknown),
                      total: format.count(data.source_coverage.total),
                    }
                  )}
                </span>
              </p>
              <VideoUsageMethodology />
              <Tabs
                value={tab}
                onValueChange={(next) => setTab(String(next))}
                className='gap-4'
              >
                <TabsList
                  aria-label={t('Video usage views')}
                  className='max-w-full flex-wrap justify-start gap-1 group-data-horizontal/tabs:h-auto'
                >
                  <TabsTrigger
                    value='current'
                    className='min-h-8 flex-none px-3 whitespace-nowrap'
                  >
                    {t('Current task records')}
                  </TabsTrigger>
                  <TabsTrigger
                    value='history'
                    className='min-h-8 flex-none px-3 whitespace-nowrap'
                  >
                    {t('Period summary')}
                  </TabsTrigger>
                  <TabsTrigger
                    value='tasks'
                    className='min-h-8 flex-none px-3 whitespace-nowrap'
                  >
                    {t('Task details')}
                  </TabsTrigger>
                </TabsList>
                <TabsContent value='current'>
                  <VideoCurrentTable models={data.models} />
                </TabsContent>
                <TabsContent value='history' className='space-y-3'>
                  <p className='text-muted-foreground text-xs'>
                    {t(
                      'Submission window [{{start}}, {{end}}); browser time zone.',
                      {
                        start: format.time(data.start_timestamp),
                        end: format.time(data.end_timestamp),
                      }
                    )}
                  </p>
                  <VideoHistoryTable models={data.models} />
                </TabsContent>
                <TabsContent value='tasks'>
                  <VideoTaskTable
                    tasks={data.tasks}
                    loading={state.report.isFetching}
                    onScopeChange={state.setScope}
                    onPageChange={state.setPage}
                  />
                </TabsContent>
              </Tabs>
            </>
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
