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
import { afterEach, expect, it } from 'vitest'

import { api } from '@/lib/api'

import { getServerLoad } from '../api'
import { appendServerSample, buildServerTrend } from '../trend'
import { serverLoadFixture } from './fixtures'

const originalAdapter = api.defaults.adapter
afterEach(() => {
  api.defaults.adapter = originalAdapter
})
it('reads only the diagnostic endpoint with cancellation and preserves unavailable counters', async () => {
  const signal = new AbortController().signal
  const fixture = serverLoadFixture()
  fixture.cpu.usage_percent = null
  api.defaults.adapter = async (config) => {
    expect(config.method).toBe('get')
    expect(config.url).toBe('/api/operations/server-load')
    expect(config.signal).toBe(signal)
    return {
      data: { success: true, data: fixture },
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    }
  }
  const result = await getServerLoad(signal)
  expect(result.cpu.usage_percent).toBeNull()
  expect(result.database.logs.in_use).toBeNull()
})
it('rejects business errors and invalid counter shapes instead of displaying healthy zero', async () => {
  let payload: unknown = { success: false, message: 'Permission denied' }
  api.defaults.adapter = async (config) => ({
    data: payload,
    status: 200,
    statusText: 'OK',
    headers: {},
    config,
  })
  await expect(getServerLoad(new AbortController().signal)).rejects.toThrow()
  payload = {
    success: true,
    data: { ...serverLoadFixture(), memory: { used_percent: -1 } },
  }
  await expect(getServerLoad(new AbortController().signal)).rejects.toThrow()
})
it('keeps only real samples within ten minutes and at most sixty points, deduplicating cached timestamps', () => {
  let samples = appendServerSample([], serverLoadFixture(1000))
  samples = appendServerSample(samples, serverLoadFixture(1000))
  expect(samples).toHaveLength(1)
  for (let index = 1; index <= 70; index++) {
    samples = appendServerSample(samples, serverLoadFixture(1000 + index * 10))
  }
  expect(samples).toHaveLength(60)
  expect(samples[0].sampled_at).toBe(1110)
  samples = appendServerSample(samples, serverLoadFixture(3000))
  expect(samples.map((sample) => sample.sampled_at)).toEqual([3000])
})
it('leaves interrupted or unavailable observations as curve gaps without creating historical measurements', () => {
  const first = serverLoadFixture(1000)
  const second = serverLoadFixture(1040)
  second.cpu.usage_percent = null
  const trend = buildServerTrend([first, second], 'cpu')
  expect(trend).toEqual([
    { timestamp: 1000, value: 27 },
    { timestamp: 1001, value: null },
    { timestamp: 1040, value: null },
  ])
})

it('accepts a CPU protection threshold above one hundred without dropping the whole snapshot', async () => {
  const fixture = serverLoadFixture()
  fixture.thresholds.cpu_percent = 150
  api.defaults.adapter = async (config) => ({
    data: { success: true, data: fixture },
    status: 200,
    statusText: 'OK',
    headers: {},
    config,
  })
  const result = await getServerLoad(new AbortController().signal)
  expect(result.thresholds.cpu_percent).toBe(150)
})
