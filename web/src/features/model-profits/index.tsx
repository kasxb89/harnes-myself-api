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
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { Maximize, Minimize } from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { PortalContainerContext } from '@/components/ui/portal-container'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { canAccessAdminRoute } from '@/lib/admin-permissions'
import { ROLE } from '@/lib/roles'
import { cn } from '@/lib/utils'
import { useAuthStore, type AuthUser } from '@/stores/auth-store'

import { getModelProfits } from './api'
import { CostRulesDialog } from './components/cost-rules-dialog'
import { ProfitDashboard } from './components/profit-dashboard'
import { ProfitFilters } from './components/profit-filters'
import { ProfitFormulas } from './components/profit-formulas'
import { ProfitFundingDetails } from './components/profit-funding-details'
import { ProfitFundingNotice } from './components/profit-funding-notice'
import { ProfitSummary } from './components/profit-summary'
import { ProfitTable } from './components/profit-table'
import { profitModelOptions } from './lib/chart-data'
import { parseProfitPeriod, profitPreset } from './lib/period'
import { useProfitFullscreen } from './lib/use-profit-fullscreen'

function profitAccessScope(
  user: AuthUser | null,
  sessionId: string | undefined
): string {
  return `${user?.id ?? ''}:${sessionId ?? ''}:${canAccessAdminRoute(user, '/model-profits')}:${Boolean(user && user.role >= ROLE.SUPER_ADMIN)}`
}

export function ModelProfitsPage() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const sessionId = useAuthStore((state) => state.auth.session?.sid)
  const client = useQueryClient()
  const [generation, setGeneration] = useState(0)
  useEffect(
    () =>
      useAuthStore.subscribe((state, previous) => {
        if (
          profitAccessScope(state.auth.user, state.auth.session?.sid) ===
          profitAccessScope(previous.auth.user, previous.auth.session?.sid)
        ) {
          return
        }
        for (const feature of ['model-profits', 'model-profit-cost-rules']) {
          const queryKey = [
            feature,
            previous.auth.user?.id,
            previous.auth.session?.sid ?? '',
          ]
          void client.cancelQueries({ queryKey })
          client.removeQueries({ queryKey })
        }
        setGeneration((value) => value + 1)
      }),
    [client]
  )
  if (!user || !canAccessAdminRoute(user, '/model-profits')) {
    return <ErrorState title={t('Access denied')} />
  }
  return (
    <ModelProfitReport
      key={`${profitAccessScope(user, sessionId)}:${generation}`}
      userId={user.id}
      sessionId={sessionId ?? ''}
      generation={generation}
    />
  )
}

function ModelProfitReport(props: {
  userId: number
  sessionId: string
  generation: number
}) {
  const { t } = useTranslation()
  const client = useQueryClient()
  useEffect(
    () => () => {
      const queryKey = [
        'model-profits',
        props.userId,
        props.sessionId,
        props.generation,
      ]
      void client.cancelQueries({ queryKey })
      client.removeQueries({ queryKey })
    },
    [client, props.userId, props.sessionId, props.generation]
  )
  const [view, setView] = useState('details')
  const fullscreen = useProfitFullscreen()
  const [draft, setDraft] = useState(() => profitPreset('month'))
  const [filters, setFilters] = useState(() => {
    const initial = parseProfitPeriod(draft)
    if (!initial) throw new Error('Invalid initial model profit period')
    return initial
  })
  const parsed = parseProfitPeriod(draft)
  const profits = useQuery({
    queryKey: [
      'model-profits',
      props.userId,
      props.sessionId,
      props.generation,
      filters,
    ],
    queryFn: ({ signal }) => getModelProfits(filters, signal),
    retry: false,
  })
  const refresh = () => {
    void profits.refetch()
  }
  return (
    <SectionPageLayout stackActionsOnMobile>
      <SectionPageLayout.Title>{t('Model profits')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <CostRulesDialog />
        <Button
          variant='outline'
          onClick={refresh}
          disabled={profits.isFetching}
        >
          {t('Refresh')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <PortalContainerContext.Provider
          value={fullscreen.active ? fullscreen.ref : undefined}
        >
          <div
            ref={fullscreen.ref}
            className={cn(
              'bg-background text-foreground flex min-w-0 flex-col gap-4',
              fullscreen.active && 'h-screen overflow-y-auto p-4 md:p-8'
            )}
          >
            {fullscreen.active && (
              <h2 className='text-xl font-semibold'>
                {t('Model profit dashboard')}
              </h2>
            )}
            <details open={!fullscreen.active}>
              <summary
                className={
                  fullscreen.active
                    ? 'cursor-pointer text-sm font-medium'
                    : 'hidden'
                }
              >
                {t('Filters')}
              </summary>
              <div className={fullscreen.active ? 'mt-3' : undefined}>
                <ProfitFilters
                  value={draft}
                  onChange={setDraft}
                  onApply={() => {
                    if (parsed) setFilters(parsed)
                  }}
                  loading={profits.isFetching}
                  invalid={!parsed}
                  models={profitModelOptions(profits.data, draft.model)}
                />
              </div>
            </details>
            <Tabs
              className='gap-4'
              value={view}
              onValueChange={(value) => setView(String(value))}
            >
              <div className='flex flex-wrap items-center justify-between gap-3'>
                <TabsList aria-label={t('Profit report view')}>
                  <TabsTrigger value='details'>
                    {t('Profit details')}
                  </TabsTrigger>
                  <TabsTrigger value='dashboard'>
                    {t('Profit dashboard')}
                  </TabsTrigger>
                </TabsList>
                {(view === 'dashboard' || fullscreen.active) && (
                  <Button
                    variant='outline'
                    size='sm'
                    onClick={() => void fullscreen.toggle()}
                    disabled={!fullscreen.supported}
                    title={
                      !fullscreen.supported
                        ? t('Fullscreen is not supported in this browser.')
                        : undefined
                    }
                    aria-pressed={fullscreen.active}
                  >
                    {fullscreen.active ? <Minimize /> : <Maximize />}
                    {fullscreen.active
                      ? t('Exit fullscreen')
                      : t('Enter fullscreen')}
                  </Button>
                )}
              </div>
              {fullscreen.failed && (
                <Alert variant='destructive'>
                  <AlertDescription>
                    {t(
                      'Unable to enter fullscreen. Use the dashboard in this window.'
                    )}
                  </AlertDescription>
                </Alert>
              )}
              {!parsed && (
                <Alert variant='destructive'>
                  <AlertDescription>
                    {t(
                      'Enter a valid start and end date; the end must not precede the start.'
                    )}
                  </AlertDescription>
                </Alert>
              )}
              {profits.isFetching && (
                <div role='status'>
                  <LoadingState
                    inline
                    message={t('Loading model profits...')}
                  />
                </div>
              )}
              {profits.isError && (
                <ErrorState
                  title={t('Failed to load model profits')}
                  onRetry={refresh}
                />
              )}
              {!profits.isError && profits.data && (
                <>
                  {profits.data.consumption_logging_enabled === false && (
                    <Alert variant='destructive'>
                      <AlertDescription>
                        {t(
                          'Consumption logging is disabled. This report may omit requests and cannot show complete profit.'
                        )}
                      </AlertDescription>
                    </Alert>
                  )}
                  <ProfitFundingNotice report={profits.data} />
                  <ProfitSummary data={profits.data.summary} />
                  <ProfitFundingDetails data={profits.data.summary} />
                  <p className='text-muted-foreground text-xs'>
                    {t(
                      'Showing {{start}} to {{end}} inclusive, Asia/Shanghai. All amounts are CNY.',
                      {
                        start: new Date(
                          filters.start_timestamp * 1000 + 8 * 3600000
                        )
                          .toISOString()
                          .slice(0, 10),
                        end: new Date(
                          (filters.end_timestamp - 1) * 1000 + 8 * 3600000
                        )
                          .toISOString()
                          .slice(0, 10),
                      }
                    )}
                  </p>
                  <TabsContent value='details'>
                    <ProfitTable rows={profits.data.rows} />
                  </TabsContent>
                  <TabsContent value='dashboard'>
                    <ProfitDashboard data={profits.data} />
                  </TabsContent>
                  <ProfitFormulas />
                </>
              )}
            </Tabs>
          </div>
        </PortalContainerContext.Provider>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
