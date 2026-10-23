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
import {
  Clock3,
  Maximize,
  Minimize,
  Pause,
  Play,
  RefreshCw,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import type { useProfitFullscreen } from '@/features/model-profits/lib/use-profit-fullscreen'
import { ActivityTimestamp } from '@/features/user-activity/display'
import { cn } from '@/lib/utils'

import type { useOperationsQuery } from '../hooks/use-operations-query'
import type { OperationsRange } from '../policy'

export function OperationsScreenToolbar(props: {
  observation: ReturnType<typeof useOperationsQuery>
  fullscreen: ReturnType<typeof useProfitFullscreen>
  paused: boolean
  onPause: () => void
  range: OperationsRange
  customRange?: boolean
  timeFiltersSupported?: boolean
  onRangeChange: (range: OperationsRange) => void
}) {
  const { t } = useTranslation()
  const query = props.observation.query
  let status = t('Sample current')
  if (!props.observation.online) status = t('Offline')
  else if (!props.observation.visible) status = t('Paused while hidden')
  else if (props.paused) status = t('Paused')
  else if (query.isFetching) status = t('Refreshing')
  else if (query.isError) status = t('Refresh failed')
  else if (!query.data) status = t('Awaiting data')
  else if (props.observation.stale) status = t('Stale sample')
  let state = 'current'
  if (!props.observation.online) state = 'offline'
  else if (!props.observation.visible || props.paused) state = 'paused'
  else if (query.isFetching) state = 'refreshing'
  else if (query.isError) state = 'failed'
  else if (!query.data) state = 'pending'
  else if (props.observation.stale) state = 'stale'
  return (
    <>
      <header className='ops-command-header'>
        <div className='ops-command-context'>
          <span className='ops-kicker'>{t('Last successful update')}</span>
          {query.dataUpdatedAt ? (
            <ActivityTimestamp value={query.dataUpdatedAt / 1000} />
          ) : (
            <span className='ops-context-empty'>
              {t('No successful sample yet')}
            </span>
          )}
        </div>
        <div className='ops-brand'>
          <p className='ops-kicker'>{t('Platform operations')}</p>
          <h1 className='ops-command-title'>{t('Operations screen')}</h1>
          <span className='ops-title-track' aria-hidden='true' />
        </div>
        <div className='ops-controls flex flex-wrap items-center gap-2'>
          <Button
            variant='outline'
            onClick={props.onPause}
            aria-pressed={props.paused}
          >
            {props.paused ? <Play aria-hidden /> : <Pause aria-hidden />}
            {props.paused ? t('Resume') : t('Pause')}
          </Button>
          <Button
            variant='outline'
            onClick={props.observation.refresh}
            disabled={
              !props.observation.online ||
              !props.observation.visible ||
              query.isFetching
            }
          >
            <RefreshCw
              aria-hidden
              className={cn(query.isFetching && 'animate-spin')}
            />
            {t('Refresh')}
          </Button>
          <Button
            variant='outline'
            onClick={() => void props.fullscreen.toggle()}
            disabled={!props.fullscreen.supported}
            aria-pressed={props.fullscreen.active}
          >
            {props.fullscreen.active ? (
              <Minimize aria-hidden />
            ) : (
              <Maximize aria-hidden />
            )}
            {props.fullscreen.active
              ? t('Exit fullscreen')
              : t('Enter fullscreen')}
          </Button>
        </div>
      </header>
      <div className='ops-observation-rail flex min-w-0 flex-wrap items-center justify-between gap-3'>
        <div
          className='ops-observation-status flex min-w-0 flex-wrap items-center gap-2'
          data-state={state}
          role='status'
          aria-label={t('Refresh status')}
        >
          <span className='ops-status-light' aria-hidden='true' />
          <Badge className='ops-status-badge' variant='secondary'>
            {status}
          </Badge>
          {props.observation.stale && status !== t('Stale sample') && (
            <Badge variant='outline'>{t('Stale sample')}</Badge>
          )}
          <span className='ops-refresh-cadence text-muted-foreground text-xs'>
            {t('Automatic refresh every {{seconds}} seconds', {
              seconds: props.observation.interval / 1000,
            })}
          </span>
        </div>
        <Combobox
          aria-label={t('Observation window')}
          className='ops-window-picker w-full sm:w-44'
          popupClassName='ops-screen-popover'
          disabled={props.timeFiltersSupported === false}
          value={props.customRange ? 'custom' : props.range}
          options={[
            ...(props.customRange
              ? [{ value: 'custom', label: t('Custom time range') }]
              : []),
            { value: '5m', label: t('Last 5 minutes') },
            { value: '1h', label: t('Last hour') },
            { value: '24h', label: t('Last 24 hours') },
          ]}
          onValueChange={(value) => {
            if (value === '5m' || value === '1h' || value === '24h') {
              props.onRangeChange(value)
            }
          }}
        />
      </div>
      <div className='ops-time-rail'>
        <div className='ops-time-cell'>
          <span className='ops-time-label'>
            <Clock3 aria-hidden='true' />
            {t('Query time window')}
          </span>
          {props.observation.window ? (
            <span className='inline-flex max-w-full flex-wrap gap-x-1'>
              <ActivityTimestamp
                value={props.observation.window.start_timestamp}
              />
              <span>—</span>
              <ActivityTimestamp
                value={props.observation.window.end_timestamp}
              />
            </span>
          ) : (
            t('Awaiting data')
          )}
        </div>
        <p className='ops-command-description'>
          {t(
            'Read-only operational samples. Only the selected tab refreshes; this is not an exact real-time stream.'
          )}
        </p>
      </div>
    </>
  )
}
