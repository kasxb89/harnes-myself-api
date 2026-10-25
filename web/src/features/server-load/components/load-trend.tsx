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
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'
import {
  CartesianGrid,
  Line,
  LineChart,
  ReferenceLine,
  XAxis,
  YAxis,
} from 'recharts'

import { EmptyState } from '@/components/empty-state'
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
} from '@/components/ui/chart'
import { ToggleGroup, ToggleGroupItem } from '@/components/ui/toggle-group'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import type { ServerLoadSnapshot } from '../api'
import { buildServerTrend, type ServerLoadMetric } from '../trend'

export function LoadTrend(props: {
  samples: ServerLoadSnapshot[]
  snapshot: ServerLoadSnapshot
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const [metric, setMetric] = useState<ServerLoadMetric>('cpu')
  const points = useMemo(
    () => buildServerTrend(props.samples, metric),
    [props.samples, metric]
  )
  const labels = { cpu: t('CPU'), memory: t('Memory'), disk: t('Storage') }
  const threshold = props.snapshot.thresholds[`${metric}_percent`]
  return (
    <section className='min-w-0 py-5' aria-label={t('Live resource trend')}>
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <div className='flex min-w-0 flex-col gap-1'>
          <h3 className='font-medium'>{t('Live resource trend')}</h3>
          <p className='text-muted-foreground text-xs'>
            {t('Observed on this page · last 10 minutes · every 10 seconds')}
          </p>
        </div>
        <ToggleGroup
          value={[metric]}
          onValueChange={(values) => {
            if (
              values[0] === 'cpu' ||
              values[0] === 'memory' ||
              values[0] === 'disk'
            ) {
              setMetric(values[0])
            }
          }}
          variant='outline'
          size='sm'
          aria-label={t('Trend metric')}
        >
          <ToggleGroupItem value='cpu'>{labels.cpu}</ToggleGroupItem>
          <ToggleGroupItem value='memory'>{labels.memory}</ToggleGroupItem>
          <ToggleGroupItem value='disk'>{labels.disk}</ToggleGroupItem>
        </ToggleGroup>
      </div>
      {points.some((point) => point.value !== null) ? (
        <ChartContainer
          className='mt-4 aspect-auto h-56 w-full'
          role='img'
          aria-label={t('{{metric}} usage trend', { metric: labels[metric] })}
          config={{ value: { label: labels[metric], color: 'var(--primary)' } }}
        >
          <LineChart
            data={points}
            margin={{ top: 12, right: 16, bottom: 4, left: 0 }}
            accessibilityLayer
          >
            <CartesianGrid vertical={false} />
            <XAxis
              dataKey='timestamp'
              type='number'
              domain={['dataMin', 'dataMax']}
              tickLine={false}
              axisLine={false}
              minTickGap={48}
              tickFormatter={(value) =>
                new Date(Number(value) * 1000).toLocaleTimeString(locale, {
                  hour: '2-digit',
                  minute: '2-digit',
                  second: '2-digit',
                })
              }
            />
            <YAxis
              domain={[0, 100]}
              width={42}
              tickLine={false}
              axisLine={false}
              tickFormatter={(value) =>
                `${formatNumber(Number(value), locale)}%`
              }
            />
            <ChartTooltip
              content={
                <ChartTooltipContent
                  labelFormatter={(value) =>
                    new Date(Number(value) * 1000).toLocaleTimeString(locale)
                  }
                  formatter={(value) =>
                    `${formatNumber(Number(value), locale)}%`
                  }
                />
              }
            />
            {props.snapshot.thresholds.guard_enabled &&
              threshold > 0 &&
              threshold <= 100 && (
                <ReferenceLine
                  y={threshold}
                  stroke='var(--destructive)'
                  strokeDasharray='4 4'
                />
              )}
            <Line
              type='linear'
              dataKey='value'
              stroke='var(--color-value)'
              strokeWidth={2}
              dot={{ r: 2 }}
              activeDot={{ r: 4 }}
              connectNulls={false}
              isAnimationActive={false}
            />
          </LineChart>
        </ChartContainer>
      ) : (
        <EmptyState
          className='min-h-56'
          title={t('Waiting for resource samples')}
          description={t(
            'Unavailable values are left blank; no historical values are generated.'
          )}
        />
      )}
    </section>
  )
}
