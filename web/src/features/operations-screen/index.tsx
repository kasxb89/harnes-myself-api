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
import { useNavigate, useSearch, Link } from '@tanstack/react-router'
import {
  Activity,
  ChartNoAxesCombined,
  Clapperboard,
  Image,
  ChevronDown,
  ExternalLink,
  Network,
  ShieldAlert,
  Users,
} from 'lucide-react'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { Main } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import { PortalContainerContext } from '@/components/ui/portal-container'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { useProfitFullscreen } from '@/features/model-profits/lib/use-profit-fullscreen'
import { ActivityTimestamp } from '@/features/user-activity/display'
import { getServerErrorMessage } from '@/lib/server-error-message'
import { cn } from '@/lib/utils'
import { useAuthStore } from '@/stores/auth-store'

import { HologramCanvas } from './components/hologram-canvas'
import { HolographicCore } from './components/holographic-core'
import { OperationsObservationFilters } from './components/observation-filters'
import { RobotBackground } from './components/robot-background'
import { OperationsScienceCharts } from './components/science-charts'
import { OperationsScreenToolbar } from './components/screen-toolbar'
import { OperationsSnapshotContent } from './components/snapshot-content'
import { useOperationsQuery } from './hooks/use-operations-query'
import {
  availableOperationsTabs,
  operationsScope,
  operationsTabs,
  operationsWindow,
  resolveOperationsTab,
  type OperationsRange,
  type OperationsTab,
  type OperationsWindow,
} from './policy'

import './operations-screen.css'

const tabIcons = {
  profit: ChartNoAxesCombined,
  concurrency: Network,
  usage: Activity,
  errors: ShieldAlert,
  image: Image,
  video: Clapperboard,
  activity: Users,
}

export function OperationsScreenPage() {
  const search = useSearch({ strict: false })
  const navigate = useNavigate()
  return (
    <OperationsScreen
      requestedTab={search.tab}
      onTabChange={(tab) =>
        void navigate({
          to: '/operations-screen',
          search: { tab },
          replace: true,
        })
      }
    />
  )
}

export function OperationsScreen(props: {
  requestedTab?: string
  onTabChange?: (tab: OperationsTab) => void
}) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const sid = useAuthStore((state) => state.auth.session?.sid)
  const tabs = availableOperationsTabs(user)
  if (!tabs.length) return <ErrorState title={t('Access denied')} />
  const scope = operationsScope(user, sid)
  return (
    <OperationsScreenReport
      key={scope}
      scope={scope}
      available={tabs}
      requestedTab={props.requestedTab}
      onTabChange={props.onTabChange}
    />
  )
}

function OperationsScreenReport(props: {
  scope: string
  available: OperationsTab[]
  requestedTab?: string
  onTabChange?: (tab: OperationsTab) => void
}) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const [selection, setSelection] = useState(props.requestedTab)
  const [range, setRange] = useState<OperationsRange>('1h')
  const [customWindow, setCustomWindow] = useState<OperationsWindow>()
  const [modelName, setModelName] = useState('')
  const [paused, setPaused] = useState(false)
  const [detailsOpen, setDetailsOpen] = useState(false)
  const fullscreen = useProfitFullscreen()
  const tab = resolveOperationsTab(selection, user) ?? props.available[0]
  const observation = useOperationsQuery({
    scope: props.scope,
    tab,
    range,
    paused,
    customWindow,
    modelName,
  })
  const query = observation.query
  const snapshot =
    observation.authorized && !observation.invalid ? query.data : undefined
  const decorationPaused =
    paused ||
    !observation.visible ||
    !observation.online ||
    observation.stale ||
    query.isError ||
    !snapshot
  useEffect(() => setSelection(props.requestedTab), [props.requestedTab])
  const changeTab = (value: unknown) => {
    const next = props.available.find((item) => item === value)
    if (next) {
      setSelection(next)
      props.onTabChange?.(next)
    }
  }
  const details = {
    profit: { to: '/model-profits' as const, search: {} },
    concurrency: {
      to: '/video-management' as const,
      search: { tab: 'concurrency' as const },
    },
    image: {
      to: '/image-management' as const,
      search: { tab: 'tasks' as const },
    },
    video: {
      to: '/video-management' as const,
      search: { tab: 'tasks' as const },
    },
    usage: {
      to: '/usage-logs/$section' as const,
      params: { section: 'common' },
      search: { type: ['2'] },
    },
    errors: {
      to: '/usage-logs/$section' as const,
      params: { section: 'errors' },
      search: {},
    },
    activity: { to: '/user-activity' as const, search: {} },
  }[tab]
  return (
    <Main className='operations-screen-host'>
      <div className='ops-screen-viewport min-h-0 flex-1 overflow-auto p-3 sm:p-4'>
        <PortalContainerContext.Provider
          value={fullscreen.active ? fullscreen.ref : undefined}
        >
          <div
            ref={fullscreen.ref}
            data-paused={decorationPaused}
            data-appearance='technology'
            className={cn(
              'operations-screen min-w-0 max-w-full overflow-x-hidden p-3 sm:p-5',
              fullscreen.active &&
                'h-screen overflow-y-auto rounded-none p-4 sm:p-8'
            )}
          >
            <RobotBackground paused={decorationPaused} />
            <div className='ops-scanline' aria-hidden='true' />
            <div className='ops-perimeter-ticks' aria-hidden='true' />
            <div className='ops-screen-layer'>
              <OperationsScreenToolbar
                observation={observation}
                fullscreen={fullscreen}
                paused={paused}
                onPause={() => setPaused((value) => !value)}
                range={range}
                customRange={Boolean(customWindow) && tab !== 'concurrency'}
                timeFiltersSupported={tab !== 'concurrency'}
                onRangeChange={(next) => {
                  setRange(next)
                  setCustomWindow(undefined)
                }}
              />
              <OperationsObservationFilters
                key={`${tab}:${range}:${customWindow?.start_timestamp ?? ''}:${customWindow?.end_timestamp ?? ''}`}
                tab={tab}
                snapshot={snapshot}
                window={observation.window ?? operationsWindow(range)}
                customWindow={customWindow}
                modelName={modelName}
                loading={query.isFetching}
                onApply={(filters) => {
                  setModelName(filters.modelName)
                  setCustomWindow(filters.customWindow)
                }}
              />
              {fullscreen.failed && (
                <Alert>
                  <AlertDescription>
                    {t(
                      'Fullscreen is unavailable. You can continue using the dashboard in this page.'
                    )}
                  </AlertDescription>
                </Alert>
              )}
              {query.isError && (
                <Alert variant='destructive'>
                  <AlertDescription className='break-words'>
                    {getServerErrorMessage(
                      query.error,
                      t('Failed to refresh operations screen')
                    )}{' '}
                    ·{' '}
                    {query.data
                      ? t(
                          'The last successful sample is retained. Automatic retries use a longer interval.'
                        )
                      : t('No successful sample yet')}
                  </AlertDescription>
                </Alert>
              )}
              <Tabs
                value={tab}
                onValueChange={changeTab}
                className='ops-section-tabs min-w-0'
              >
                <div className='ops-tab-scroll'>
                  <TabsList
                    aria-label={t('Operations screen sections')}
                    className='ops-tab-strip h-auto min-w-full justify-start gap-1 group-data-horizontal/tabs:h-auto'
                  >
                    {operationsTabs
                      .filter((item) => props.available.includes(item.value))
                      .map((item, index) => {
                        const Icon = tabIcons[item.value]
                        return (
                          <TabsTrigger
                            key={item.value}
                            value={item.value}
                            className='ops-tab min-h-10 min-w-24 flex-none px-3 sm:px-4'
                          >
                            <span className='ops-tab-index' aria-hidden='true'>
                              {String(index + 1).padStart(2, '0')}
                            </span>
                            <Icon aria-hidden='true' />
                            {t(item.labelKey)}
                          </TabsTrigger>
                        )
                      })}
                  </TabsList>
                </div>
                <TabsContent
                  value={tab}
                  className='ops-screen-panel min-w-0 space-y-5 pt-3'
                >
                  <section
                    className='ops-hud-deck'
                    aria-label={t('Operations screen')}
                  >
                    <div className='ops-hud-core-slot'>
                      <HolographicCore
                        snapshot={snapshot}
                        paused={decorationPaused}
                        stale={observation.stale || query.isError}
                      />
                    </div>
                    <div className='ops-hud-flank ops-hud-flank--left'>
                      {snapshot ? (
                        <OperationsScienceCharts
                          snapshot={snapshot}
                          slot='left'
                        />
                      ) : (
                        <div className='ops-hud-pending'>
                          {query.isPending && query.isFetching ? (
                            <LoadingState inline />
                          ) : (
                            <EmptyState title={t('No successful sample yet')} />
                          )}
                        </div>
                      )}
                    </div>
                    <HologramCanvas paused={decorationPaused} />
                    <div className='ops-hud-flank ops-hud-flank--right'>
                      {snapshot ? (
                        <OperationsScienceCharts
                          snapshot={snapshot}
                          slot='right'
                        />
                      ) : (
                        <div className='ops-hud-pending'>
                          {t('Awaiting data')}
                        </div>
                      )}
                    </div>
                    <span
                      className='ops-hud-link ops-hud-link--left'
                      aria-hidden='true'
                    />
                    <span
                      className='ops-hud-link ops-hud-link--right'
                      aria-hidden='true'
                    />
                  </section>
                  {query.isError && query.data && (
                    <p className='text-muted-foreground text-xs'>
                      {t('Displayed sample window')}:{' '}
                      <ActivityTimestamp
                        value={query.data.window.start_timestamp}
                      />{' '}
                      —{' '}
                      <ActivityTimestamp
                        value={query.data.window.end_timestamp}
                      />
                    </p>
                  )}
                  <Collapsible
                    className='ops-details-console'
                    open={detailsOpen}
                    onOpenChange={setDetailsOpen}
                  >
                    <div className='ops-detail-row'>
                      <CollapsibleTrigger
                        disabled={!snapshot}
                        render={
                          <Button
                            className='ops-details-trigger'
                            variant='ghost'
                          />
                        }
                      >
                        <ChevronDown aria-hidden='true' />
                        {t('Details')}
                      </CollapsibleTrigger>
                      <Button
                        className='ops-detail-link'
                        variant='ghost'
                        render={<Link {...details} />}
                      >
                        <ExternalLink aria-hidden='true' />
                        {t('Open detailed filters')}
                      </Button>
                    </div>
                    <CollapsibleContent className='ops-details-content'>
                      {snapshot && (
                        <OperationsSnapshotContent
                          snapshot={snapshot}
                          showCharts={false}
                        />
                      )}
                    </CollapsibleContent>
                  </Collapsible>
                </TabsContent>
              </Tabs>
            </div>
          </div>
        </PortalContainerContext.Provider>
      </div>
    </Main>
  )
}
