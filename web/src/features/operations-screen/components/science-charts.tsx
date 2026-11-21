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
import { ChartNoAxesCombined, ChevronDown } from 'lucide-react'
import { useId, useMemo } from 'react'
import { useTranslation } from 'react-i18next'
import {
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Pie,
  PieChart,
  ReferenceLine,
  XAxis,
  YAxis,
} from 'recharts'

import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import {
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from '@/components/ui/chart'
import { formatProfitCny } from '@/features/model-profits/lib/format'
import { getErrorDiagnosticCopy } from '@/features/usage-logs/lib/error-diagnostic-copy'
import { useMediaQuery } from '@/hooks/use-media-query'
import { toIntlLocale } from '@/i18n/languages'
import { formatCompactNumber, formatNumber } from '@/lib/format'

import type { OperationsSnapshot } from '../api'
import {
  operationsChartData,
  type ScienceChartData,
  type ScienceChartRow,
} from '../chart-data'

const colors = ['cyan', 'blue', 'magenta', 'green', 'amber'] as const
const unitKeys = {
  tasks: 'Task records',
  records: 'Log records',
  events: 'Billing events',
  tokens: 'Tokens',
  slots: 'Slots',
  processes: 'Client processes',
} as const
const sampleKeys = {
  logs: 'Chart sample: {{shown}} returned log records of {{total}} matches. Settlement records are not distinct API calls.',
  clients:
    'Chart sample: {{shown}} returned client processes of {{total}} matches. Current presence is global, not a history-window total.',
} as const

function ScienceChart({ chart }: { chart: ScienceChartData }) {
  const { t, i18n } = useTranslation()
  const headingId = useId()
  const noteId = useId()
  const reducedMotion = useMediaQuery('(prefers-reduced-motion: reduce)')
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const title = t(chart.titleKey)
  const label = (row: ScienceChartRow) => {
    if (row.errorReason) return getErrorDiagnosticCopy(row.errorReason, t).title
    return row.translate ? t(row.label) : row.label
  }
  const value = (number: unknown) => {
    if (typeof number !== 'number' || !Number.isFinite(number)) {
      return t('Unknown')
    }
    return chart.unit === 'cny'
      ? formatProfitCny(number, locale)
      : formatNumber(number, locale)
  }
  const unit = chart.unit === 'cny' ? 'CNY (¥)' : t(unitKeys[chart.unit])
  const rows = chart.rows.map((row, index) => ({
    ...row,
    label: label(row),
    chartKey: `slice${index}`,
    ...(chart.kind === 'donut'
      ? { fill: `var(--ops-${colors[index % colors.length]})` }
      : {}),
  }))
  const config: ChartConfig =
    chart.kind === 'bar'
      ? Object.fromEntries(
          chart.series.map((series) => [
            series.key,
            { label: t(series.labelKey), color: `var(--ops-${series.color})` },
          ])
        )
      : Object.fromEntries(
          rows.map((row) => [
            row.chartKey,
            { label: row.label, color: row.fill },
          ])
        )
  const numbers = rows
    .flatMap((row) => chart.series.map((series) => row[series.key]))
    .filter(
      (number): number is number =>
        typeof number === 'number' && Number.isFinite(number)
    )
  const hasUnknown = rows.some((row) =>
    chart.series.some(
      (series) =>
        typeof row[series.key] !== 'number' || !Number.isFinite(row[series.key])
    )
  )
  const hasPlot =
    rows.length > 0 &&
    numbers.length > 0 &&
    (chart.kind === 'bar' ||
      (!hasUnknown && numbers.some((number) => number > 0)))
  let empty =
    chart.emptyKey && !rows.length ? chart.emptyKey : 'No chart observations'
  if (
    rows.length &&
    numbers.length &&
    numbers.every((number) => number === 0) &&
    !hasUnknown
  ) {
    empty = 'All observed counts are zero'
  }
  const minimum = Math.min(0, ...numbers)
  const maximum = Math.max(0, ...numbers)
  const domain: [number, number] = [
    minimum,
    maximum === minimum ? minimum + 1 : maximum,
  ]
  const chartHeight =
    chart.kind === 'bar'
      ? Math.max(260, rows.length * (chart.series.length > 2 ? 40 : 30) + 60)
      : 260
  const columns: StaticDataTableColumn<(typeof rows)[number]>[] = [
    {
      id: 'category',
      header: t(chart.categoryKey),
      cell: (row) => (
        <span className='block max-w-64 break-all'>{row.label}</span>
      ),
    },
    ...chart.series.map((series) => ({
      id: series.key,
      header: `${t(series.labelKey)} · ${unit}`,
      className: 'text-right tabular-nums',
      cell: (row: (typeof rows)[number]) => value(row[series.key]),
    })),
  ]
  const noteValues = Object.fromEntries(
    Object.entries(
      chart.noteValues ??
        (chart.sample
          ? { shown: chart.sample.shown, total: chart.sample.total }
          : {})
    ).map(([key, number]) => [key, formatNumber(number, locale)])
  )
  const tooltip = (
    <ChartTooltip
      filterNull={false}
      content={
        <ChartTooltipContent
          className='ops-chart-tooltip text-foreground border-(--ops-grid) bg-(--ops-tooltip-bg)'
          hideLabel={chart.kind === 'donut'}
          formatter={(number, name, item) => (
            <>
              <span
                className='h-2 w-2 shrink-0 rounded-sm'
                style={{ background: item.color ?? item.payload?.fill }}
              />
              <span className='flex-1'>
                {config[String(name)]?.label ?? name}
              </span>
              <span className='font-mono tabular-nums'>{value(number)}</span>
            </>
          )}
        />
      }
    />
  )
  return (
    <section
      className='ops-chart-panel flex min-w-0 flex-col rounded-xl border p-4 sm:p-5'
      aria-labelledby={headingId}
      aria-describedby={noteId}
      data-chart-kind={chart.kind}
    >
      <header className='ops-chart-header flex flex-col gap-2.5'>
        <div className='ops-chart-heading flex w-full items-start justify-between gap-4'>
          <h3
            id={headingId}
            className='ops-chart-title min-w-0 text-sm leading-6 font-semibold'
          >
            {title}
          </h3>
          <span
            className='ops-chart-unit shrink-0 rounded-full border border-(--ops-grid) px-2.5 py-1 text-xs leading-4'
            aria-label={`${t('Unit')}: ${unit}`}
          >
            {unit}
          </span>
        </div>
        {chart.unknownPools !== undefined && chart.unknownPools > 0 && (
          <p className='ops-chart-note text-xs'>
            {t('Unknown upstream pools: {{count}}', {
              count: chart.unknownPools,
              formatParams: { count: { locale } },
            })}
          </p>
        )}
        {hasUnknown && (
          <p className='ops-chart-note text-xs'>
            {t('Unknown values are not plotted as zero.')}
          </p>
        )}
      </header>
      {hasPlot ? (
        <ChartContainer
          className='ops-chart-canvas mt-3 aspect-auto w-full'
          style={{ height: chartHeight }}
          config={config}
          initialDimension={{ width: 560, height: chartHeight }}
          role='img'
          aria-label={`${title} · ${unit}`}
        >
          {chart.kind === 'bar' ? (
            <BarChart
              data={rows}
              layout='vertical'
              margin={{ top: 8, right: 20, left: 0, bottom: 12 }}
              barGap={3}
              accessibilityLayer
            >
              <CartesianGrid
                horizontal={false}
                stroke='var(--ops-grid)'
                strokeOpacity={0.55}
              />
              <XAxis
                type='number'
                domain={domain}
                allowDecimals={chart.unit === 'cny'}
                minTickGap={28}
                tick={{ fill: 'var(--ops-axis)', fontSize: 12 }}
                tickLine={false}
                axisLine={false}
                tickFormatter={(number) =>
                  `${chart.unit === 'cny' ? '¥' : ''}${formatCompactNumber(number, locale)}`
                }
                label={{
                  value: unit,
                  position: 'insideBottomRight',
                  offset: -8,
                  fill: 'var(--ops-axis)',
                  fontSize: 11,
                }}
              />
              <YAxis
                type='category'
                dataKey='label'
                width={140}
                tick={{ fill: 'var(--ops-axis)', fontSize: 12 }}
                tickLine={false}
                axisLine={false}
                interval={0}
                tickFormatter={(text: string) =>
                  text.length > 18 ? `${text.slice(0, 17)}…` : text
                }
              />
              <ReferenceLine x={0} stroke='var(--ops-axis)' />
              {tooltip}
              <ChartLegend
                content={
                  <ChartLegendContent className='ops-chart-legend flex-wrap justify-start text-xs leading-5' />
                }
              />
              {chart.series.map((series) => (
                <Bar
                  key={series.key}
                  dataKey={series.key}
                  fill={`var(--color-${series.key})`}
                  radius={2}
                  maxBarSize={16}
                  isAnimationActive={!reducedMotion}
                  animationDuration={240}
                />
              ))}
            </BarChart>
          ) : (
            <PieChart accessibilityLayer>
              {tooltip}
              <ChartLegend
                content={
                  <ChartLegendContent
                    className='ops-chart-legend flex-wrap text-xs leading-5'
                    nameKey='chartKey'
                  />
                }
              />
              <Pie
                data={rows}
                dataKey='value'
                nameKey='chartKey'
                innerRadius='58%'
                outerRadius='82%'
                paddingAngle={2}
                cornerRadius={3}
                stroke='var(--ops-tooltip-bg)'
                strokeWidth={2}
                isAnimationActive={!reducedMotion}
                animationDuration={240}
              >
                {rows.map((row) => (
                  <Cell key={row.id} fill={row.fill} />
                ))}
              </Pie>
            </PieChart>
          )}
        </ChartContainer>
      ) : (
        <div
          className='ops-chart-canvas flex items-center justify-center text-sm'
          style={{ height: chartHeight }}
          role='status'
        >
          <EmptyState
            icon={ChartNoAxesCombined}
            title={t(empty)}
            className='ops-chart-empty'
          />
        </div>
      )}
      <details className='ops-chart-details group mt-4 min-w-0 border-t border-(--ops-grid) text-xs'>
        <summary className='flex min-h-11 cursor-pointer list-none items-center justify-between gap-3 rounded-sm py-2 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-(--ops-axis)'>
          <span>{t('Chart values')}</span>
          <ChevronDown
            className='size-3.5 shrink-0 transition-transform duration-150 group-open:rotate-180 motion-reduce:transition-none'
            aria-hidden='true'
          />
        </summary>
        <div className='ops-chart-details-content space-y-4 pb-1'>
          <div id={noteId} className='ops-chart-note space-y-2 text-xs'>
            <p className='font-medium'>{t('Scope')}</p>
            <p>{t(chart.noteKey, noteValues)}</p>
            {chart.sample &&
              chart.noteKey !== sampleKeys[chart.sample.kind] && (
                <p>
                  {t(sampleKeys[chart.sample.kind], {
                    shown: formatNumber(chart.sample.shown, locale),
                    total: formatNumber(chart.sample.total, locale),
                  })}
                </p>
              )}
          </div>
          <StaticDataTable
            columns={columns}
            data={rows}
            getRowKey={(row) => row.id}
            tableProps={{
              'aria-label': `${t('Chart values')} · ${title}`,
            }}
            emptyContent={t(chart.emptyKey ?? 'No chart observations')}
          />
        </div>
      </details>
    </section>
  )
}

export function OperationsScienceCharts({
  snapshot,
  slot,
}: {
  snapshot: OperationsSnapshot
  slot?: 'left' | 'right'
}) {
  const charts = useMemo(() => operationsChartData(snapshot), [snapshot])
  if (slot) {
    const chart = charts[slot === 'left' ? 0 : 1]
    return <ScienceChart key={chart.id} chart={chart} />
  }
  return (
    <div className='ops-chart-grid grid min-w-0 gap-4 xl:grid-cols-2'>
      {charts.map((chart) => (
        <ScienceChart key={chart.id} chart={chart} />
      ))}
    </div>
  )
}
