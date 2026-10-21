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
import {
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Legend,
  Line,
  ComposedChart,
  Pie,
  PieChart,
  XAxis,
  YAxis,
} from 'recharts'

import { EmptyState } from '@/components/empty-state'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
} from '@/components/ui/chart'
import { toIntlLocale } from '@/i18n/languages'
import { formatCompactNumber, formatNumber } from '@/lib/format'

import { profitChartRows } from '../lib/chart-data'
import { formatProfitCny } from '../lib/format'
import type { ModelProfitResult } from '../types'

const chartColors = [
  'var(--chart-1)',
  'var(--chart-2)',
  'var(--chart-3)',
  'var(--chart-4)',
  'var(--chart-5)',
  'var(--muted-foreground)',
]

export function ProfitDashboard(props: { data: ModelProfitResult }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const label = (row: { model_name: string; other: boolean }) =>
    row.other ? t('Other models') : row.model_name || t('Unknown model')
  const revenue = profitChartRows(props.data.rows, 'revenue_cny', 10).map(
    (row) => ({
      ...row,
      label: label(row),
    })
  )
  const requests = profitChartRows(
    props.data.rows.filter((row) => row.requests > 0),
    'requests',
    5
  ).map((row) => ({ ...row, label: label(row) }))
  const trend = [...(props.data.trend ?? [])].sort(
    (a, b) => a.bucket_start - b.bucket_start
  )
  const date = (timestamp: number, full = false) =>
    new Intl.DateTimeFormat(locale, {
      timeZone: 'Asia/Shanghai',
      year: full ? 'numeric' : undefined,
      month: '2-digit',
      day: '2-digit',
    }).format(new Date(timestamp * 1000))
  const revenueConfig = {
    revenue_cny: { label: t('Billed revenue'), color: 'var(--chart-1)' },
  }
  const requestConfig = {
    requests: { label: t('Requests'), color: 'var(--chart-2)' },
  }
  const trendConfig = { ...revenueConfig, ...requestConfig }

  if (props.data.rows.length === 0) {
    return (
      <EmptyState
        title={t('No model consumption in this period')}
        description={t('Choose another period or model.')}
      />
    )
  }

  return (
    <div className='grid min-w-0 gap-4 lg:grid-cols-2 2xl:grid-cols-3'>
      <Card className='min-w-0'>
        <CardHeader>
          <CardTitle>{t('Model revenue ranking')}</CardTitle>
          <CardDescription>
            {t(
              'Top 10 models by billed revenue; remaining models are grouped together.'
            )}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <ChartContainer
            config={revenueConfig}
            className='h-80 w-full'
            role='img'
            aria-label={t('Model revenue ranking')}
          >
            <BarChart
              accessibilityLayer
              data={revenue}
              layout='vertical'
              margin={{ left: 0, right: 16 }}
            >
              <CartesianGrid horizontal={false} stroke='var(--border)' />
              <XAxis
                type='number'
                tick={{ fill: 'var(--muted-foreground)' }}
                tickFormatter={(value: number) =>
                  formatCompactNumber(value, locale)
                }
              />
              <YAxis
                type='category'
                tick={{ fill: 'var(--muted-foreground)' }}
                dataKey='label'
                width={110}
                tickLine={false}
                axisLine={false}
                tickFormatter={(value: string) =>
                  value.length > 17 ? `${value.slice(0, 16)}…` : value
                }
              />
              <ChartTooltip
                content={
                  <ChartTooltipContent
                    formatter={(value) => (
                      <div className='flex w-full justify-between gap-4'>
                        <span>{t('Billed revenue')}</span>
                        <span className='font-medium tabular-nums'>
                          {formatProfitCny(Number(value), locale)}
                        </span>
                      </div>
                    )}
                  />
                }
              />
              <Bar
                dataKey='revenue_cny'
                fill='var(--color-revenue_cny)'
                radius={4}
                isAnimationActive={false}
              />
            </BarChart>
          </ChartContainer>
          <details className='mt-3 text-xs'>
            <summary className='text-muted-foreground cursor-pointer'>
              {t('View model values')}
            </summary>
            <ol
              className='mt-3 grid gap-2 text-xs'
              aria-label={t('Revenue ranking values')}
            >
              {revenue.map((row) => (
                <li
                  key={row.id}
                  className='flex min-w-0 items-start justify-between gap-3'
                >
                  <span className='min-w-0 break-all'>{row.label}</span>
                  <span className='shrink-0 font-medium tabular-nums'>
                    {formatProfitCny(row.revenue_cny, locale)}
                  </span>
                </li>
              ))}
            </ol>
          </details>
        </CardContent>
      </Card>
      <Card className='min-w-0'>
        <CardHeader>
          <CardTitle>{t('Request distribution')}</CardTitle>
          <CardDescription>
            {t('Requests by model within the same applied filters.')}
          </CardDescription>
        </CardHeader>
        <CardContent>
          {requests.length === 0 ? (
            <EmptyState title={t('No requests to display')} />
          ) : (
            <>
              <ChartContainer
                config={requestConfig}
                className='h-80 w-full'
                role='img'
                aria-label={t('Request distribution')}
              >
                <PieChart accessibilityLayer>
                  <ChartTooltip
                    content={
                      <ChartTooltipContent
                        hideLabel
                        formatter={(value, name) => (
                          <div className='flex w-full justify-between gap-4'>
                            <span>{String(name)}</span>
                            <span className='font-medium tabular-nums'>
                              {formatNumber(Number(value), locale)}
                            </span>
                          </div>
                        )}
                      />
                    }
                  />
                  <Pie
                    data={requests}
                    dataKey='requests'
                    nameKey='label'
                    innerRadius='52%'
                    outerRadius='80%'
                    paddingAngle={requests.length > 1 ? 2 : 0}
                    isAnimationActive={false}
                  >
                    {requests.map((row, index) => (
                      <Cell
                        key={row.id}
                        fill={chartColors[index % chartColors.length]}
                      />
                    ))}
                  </Pie>
                </PieChart>
              </ChartContainer>
              <ul
                className='mt-3 grid gap-2 text-xs sm:grid-cols-2'
                aria-label={t('Request distribution values')}
              >
                {requests.map((row, index) => (
                  <li
                    key={row.id}
                    className='flex min-w-0 items-start justify-between gap-3'
                  >
                    <span className='flex min-w-0 items-start gap-2'>
                      <span
                        className='mt-1 size-2 shrink-0 rounded-full'
                        style={{
                          backgroundColor:
                            chartColors[index % chartColors.length],
                        }}
                        aria-hidden
                      />
                      <span className='min-w-0 break-all'>{row.label}</span>
                    </span>
                    <span className='shrink-0 font-medium tabular-nums'>
                      {formatNumber(row.requests, locale)}
                    </span>
                  </li>
                ))}
              </ul>
            </>
          )}
        </CardContent>
      </Card>
      <Card className='min-w-0 lg:col-span-2 2xl:col-span-1'>
        <CardHeader>
          <CardTitle>{t('Daily revenue and requests')}</CardTitle>
          <CardDescription>
            {t(
              'Shanghai calendar days; revenue and requests use separate axes. Revenue includes pending reserved charges; gross profit covers only the known-cost portion.'
            )}
          </CardDescription>
        </CardHeader>
        <CardContent>
          {props.data.trend_granularity !== 'day' || trend.length === 0 ? (
            <EmptyState
              title={t('Daily trend is unavailable for this report')}
            />
          ) : (
            <>
              <ChartContainer
                config={trendConfig}
                className='h-80 w-full'
                role='img'
                aria-label={t('Daily revenue and requests')}
              >
                <ComposedChart
                  accessibilityLayer
                  data={trend}
                  margin={{ left: 4, right: 4 }}
                >
                  <CartesianGrid vertical={false} stroke='var(--border)' />
                  <XAxis
                    dataKey='bucket_start'
                    tick={{ fill: 'var(--muted-foreground)' }}
                    tickFormatter={(value: number) => date(value)}
                    minTickGap={28}
                    tickLine={false}
                  />
                  <YAxis
                    yAxisId='revenue'
                    tick={{ fill: 'var(--muted-foreground)' }}
                    tickFormatter={(value: number) =>
                      formatCompactNumber(value, locale)
                    }
                    width={55}
                  />
                  <YAxis
                    yAxisId='requests'
                    orientation='right'
                    tick={{ fill: 'var(--muted-foreground)' }}
                    allowDecimals={false}
                    tickFormatter={(value: number) =>
                      formatCompactNumber(value, locale)
                    }
                    width={45}
                  />
                  <ChartTooltip
                    content={
                      <ChartTooltipContent
                        labelFormatter={(_, payload) =>
                          date(Number(payload[0]?.payload.bucket_start), true)
                        }
                        formatter={(value, name) => (
                          <div className='flex w-full justify-between gap-4'>
                            <span>
                              {name === 'revenue_cny'
                                ? t('Billed revenue')
                                : t('Requests')}
                            </span>
                            <span className='font-medium tabular-nums'>
                              {name === 'revenue_cny'
                                ? formatProfitCny(Number(value), locale)
                                : formatNumber(Number(value), locale)}
                            </span>
                          </div>
                        )}
                      />
                    }
                  />
                  <Legend
                    formatter={(value: string) =>
                      value === 'revenue_cny'
                        ? t('Billed revenue')
                        : t('Requests')
                    }
                  />
                  <Bar
                    yAxisId='revenue'
                    dataKey='revenue_cny'
                    fill='var(--color-revenue_cny)'
                    radius={[4, 4, 0, 0]}
                    isAnimationActive={false}
                  />
                  <Line
                    yAxisId='requests'
                    dataKey='requests'
                    stroke='var(--color-requests)'
                    strokeWidth={2}
                    dot={false}
                    isAnimationActive={false}
                  />
                </ComposedChart>
              </ChartContainer>
              <details className='mt-3 text-xs'>
                <summary className='text-muted-foreground cursor-pointer'>
                  {t('View daily values')}
                </summary>
                <ul
                  className='mt-3 grid max-h-64 gap-2 overflow-y-auto'
                  aria-label={t('Daily trend values')}
                >
                  {trend.map((point) => (
                    <li
                      key={point.bucket_start}
                      className='flex flex-wrap justify-between gap-2 border-b pb-2 tabular-nums'
                    >
                      <span>{date(point.bucket_start, true)}</span>
                      <span>
                        {t('Billed revenue')}:{' '}
                        {formatProfitCny(point.revenue_cny, locale)}
                      </span>
                      <span>
                        {t('Requests')}: {formatNumber(point.requests, locale)}
                      </span>
                    </li>
                  ))}
                </ul>
              </details>
            </>
          )}
        </CardContent>
      </Card>
    </div>
  )
}
