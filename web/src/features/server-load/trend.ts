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
import type { ServerLoadSnapshot } from './api'

export type ServerLoadMetric = 'cpu' | 'memory' | 'disk'
export interface ServerTrendPoint {
  timestamp: number
  value: number | null
}

// A page-local window of observations, never a fabricated historical series.
export function appendServerSample(
  samples: ServerLoadSnapshot[],
  incoming: ServerLoadSnapshot
): ServerLoadSnapshot[] {
  const latest = samples.at(-1)
  if (latest && incoming.sampled_at < latest.sampled_at) return samples
  return [
    ...samples.filter(
      (sample) =>
        sample.sampled_at > incoming.sampled_at - 600 &&
        sample.sampled_at !== incoming.sampled_at
    ),
    incoming,
  ].slice(-60)
}

export function buildServerTrend(
  samples: ServerLoadSnapshot[],
  metric: ServerLoadMetric
): ServerTrendPoint[] {
  const result: ServerTrendPoint[] = []
  for (const sample of samples) {
    const previous = result.at(-1)
    // A null separator marks a collection gap, not a sampled zero.
    if (previous && sample.sampled_at - previous.timestamp > 15) {
      result.push({ timestamp: previous.timestamp + 1, value: null })
    }
    result.push({
      timestamp: sample.sampled_at,
      value:
        metric === 'cpu'
          ? sample.cpu.usage_percent
          : sample[metric].used_percent,
    })
  }
  return result
}
