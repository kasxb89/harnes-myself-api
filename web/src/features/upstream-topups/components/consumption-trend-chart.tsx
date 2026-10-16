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
import { VChart } from '@visactor/react-vchart'

import { Skeleton } from '@/components/ui/skeleton'
import { getCurrencyDisplay } from '@/lib/currency'
import { formatChartTime } from '@/lib/time'
import { useChartTheme } from '@/lib/use-chart-theme'
import { VCHART_OPTION } from '@/lib/vchart'

/** One day of consumption, as the ledger endpoint reports it. */
export type DailyConsumptionPoint = {
  day_start: number
  quota: number
  calls: number
}

type ConsumptionTrendChartProps = {
  points: DailyConsumptionPoint[]
}

/**
 * Consumption over the last two weeks: one series on one axis, in the
 * deployment's display currency. Call counts are deliberately not drawn here —
 * a second measure on a second scale would make both series read wrong.
 */
export function ConsumptionTrendChart(props: ConsumptionTrendChartProps) {
  const { resolvedTheme, themeReady } = useChartTheme()
  const { config, meta } = getCurrencyDisplay()
  const rate = 'exchangeRate' in meta ? meta.exchangeRate : 1

  const values = props.points.map((point) => ({
    Time: formatChartTime(point.day_start),
    Usage: (point.quota / config.quotaPerUnit) * rate,
  }))

  const spec = {
    type: 'area',
    data: [{ id: 'consumption', values }],
    xField: 'Time',
    yField: 'Usage',
    line: { style: { lineWidth: 2 } },
    point: { visible: true, size: 8 },
    axes: [
      {
        orient: 'bottom',
        label: { autoHide: true, autoLimit: true },
      },
      {
        orient: 'left',
        label: {
          formatMethod: (value: number) => value.toFixed(2),
        },
      },
    ],
  }

  if (!themeReady) return <Skeleton className='h-full w-full' />

  return (
    <VChart
      key={`consumption-trend-${resolvedTheme}`}
      spec={{
        ...spec,
        theme: resolvedTheme === 'dark' ? 'dark' : 'light',
        background: 'transparent',
      }}
      option={VCHART_OPTION}
    />
  )
}
