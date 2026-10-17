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
import { formatChartTime } from '@/lib/time'
import { useChartTheme } from '@/lib/use-chart-theme'
import { VCHART_OPTION } from '@/lib/vchart'

export type DailyBarPoint = {
  day_start: number
  value: number
}

type DailyBarChartProps = {
  points: DailyBarPoint[]
  /** Axis tick text; the measure differs per chart (count vs money). */
  tickFormat: (value: number) => string
  /** Bars must not imply sub-unit precision for whole counts. */
  minInterval?: number
}

/** One measure per day as bars: identity comes from the card title, so no legend. */
export function DailyBarChart(props: DailyBarChartProps) {
  const { resolvedTheme, themeReady } = useChartTheme()

  const values = props.points.map((point) => ({
    Time: formatChartTime(point.day_start),
    Value: point.value,
  }))

  const spec = {
    type: 'bar',
    data: [{ id: 'daily', values }],
    xField: 'Time',
    yField: 'Value',
    bar: { style: { cornerRadius: 4 }, state: { hover: { opacity: 0.8 } } },
    axes: [
      {
        orient: 'bottom',
        label: { autoHide: true, autoLimit: true },
      },
      {
        orient: 'left',
        minInterval: props.minInterval,
        label: { formatMethod: props.tickFormat },
      },
    ],
  }

  if (!themeReady) return <Skeleton className='h-full w-full' />

  return (
    <VChart
      key={`daily-bar-${resolvedTheme}`}
      spec={{
        ...spec,
        theme: resolvedTheme === 'dark' ? 'dark' : 'light',
        background: 'transparent',
      }}
      option={VCHART_OPTION}
    />
  )
}
