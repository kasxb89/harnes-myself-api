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
import { useTranslation } from 'react-i18next'
import { CartesianGrid, Line, LineChart, XAxis, YAxis } from 'recharts'

import { EmptyState } from '@/components/empty-state'
import {
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
} from '@/components/ui/chart'

import { useVideoFormat } from '../lib/format'
import type { VideoIngressPoint, VideoTrendPoint } from '../schema'

type Kind = 'acceptance' | 'peak' | 'ingress'
type Point = VideoTrendPoint | VideoIngressPoint
const seriesKeys = {
  acceptance: ['accepted_task_rate'],
  peak: ['recorded_peak'],
  ingress: ['submit_qps', 'status_qps'],
} as const
export function VideoTrendChart(props: {
  kind: Kind
  points: Point[]
  title: string
}) {
  const { t } = useTranslation()
  const format = useVideoFormat()
  const config = {
    accepted_task_rate: {
      label: t('Accepted tasks / s'),
      color: 'var(--chart-1)',
    },
    recorded_peak: {
      label: t('Recorded in-flight peak'),
      color: 'var(--chart-2)',
    },
    submit_qps: { label: t('Submit POST QPS'), color: 'var(--chart-1)' },
    status_qps: { label: t('Status GET QPS'), color: 'var(--chart-3)' },
  }
  const last = props.points.at(-1)
  if (!last) {
    return <EmptyState title={t('No trend buckets available')} />
  }
  // Two display endpoints preserve every bucket's width, including an isolated
  // known bucket between null neighbors. This does not create measurements.
  const chartPoints = props.points.flatMap((point) => [
    { ...point, chart_timestamp: point.start_timestamp },
    { ...point, chart_timestamp: point.end_timestamp },
  ])
  return (
    <ChartContainer
      config={config}
      className='h-72 w-full'
      role='img'
      aria-label={props.title}
    >
      <LineChart
        accessibilityLayer
        data={chartPoints}
        margin={{ left: 0, right: 12, top: 8, bottom: 8 }}
      >
        <CartesianGrid vertical={false} stroke='var(--border)' />
        <XAxis
          dataKey='chart_timestamp'
          tick={{ fill: 'var(--muted-foreground)' }}
          type='number'
          domain={[props.points[0].start_timestamp, last.end_timestamp]}
          tickFormatter={(value: number) =>
            format.tick(
              value,
              last.end_timestamp - props.points[0].start_timestamp <= 3600
            )
          }
          minTickGap={48}
          tickLine={false}
          axisLine={false}
        />
        <YAxis
          width={55}
          tick={{ fill: 'var(--muted-foreground)' }}
          tickFormatter={(value: number) =>
            props.kind === 'peak' ? format.count(value) : format.rate(value)
          }
          allowDecimals={props.kind !== 'peak'}
          tickLine={false}
          axisLine={false}
        />
        <ChartTooltip
          filterNull={false}
          content={
            <ChartTooltipContent
              labelFormatter={(_, payload) => {
                const point = payload[0]?.payload as Point | undefined
                if (!point) return null
                return (
                  <div className='max-w-72 space-y-1 whitespace-normal'>
                    <div>
                      {t('Bucket [{{start}}, {{end}})', {
                        start: format.time(point.start_timestamp),
                        end: format.time(point.end_timestamp),
                      })}
                    </div>
                    <div>
                      {t('Actual bucket seconds')}:{' '}
                      {format.duration(point.seconds)}
                    </div>
                    {'accepted_tasks' in point && (
                      <div>
                        {t('Accepted tasks')}:{' '}
                        {format.count(point.accepted_tasks)}
                      </div>
                    )}
                    {props.kind === 'peak' && 'peak_exclusions' in point && (
                      <div>{format.exclusions(point.peak_exclusions)}</div>
                    )}
                    {'covered_seconds' in point && (
                      <div>
                        {t('Covered / bucket seconds')}:{' '}
                        {format.count(point.covered_seconds)} /{' '}
                        {format.count(point.seconds)}
                      </div>
                    )}
                  </div>
                )
              }}
              formatter={(value, name) => {
                const key = String(name) as keyof typeof config
                if (!Object.hasOwn(config, key)) return null
                return (
                  <div className='flex w-full justify-between gap-3'>
                    <span>{config[key].label}</span>
                    <span className='font-medium tabular-nums'>
                      {typeof value === 'number'
                        ? format.rate(value)
                        : t('Unknown')}
                    </span>
                  </div>
                )
              }}
            />
          }
        />
        <ChartLegend
          content={<ChartLegendContent className='flex-wrap text-xs' />}
        />
        {seriesKeys[props.kind].map((key) => (
          <Line
            key={key}
            name={key}
            dataKey={key}
            type='stepAfter'
            stroke={`var(--color-${key})`}
            strokeWidth={2}
            dot={props.points.length <= 2}
            connectNulls={false}
            isAnimationActive={false}
          />
        ))}
      </LineChart>
    </ChartContainer>
  )
}
