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

import { emptyMediaPayload } from '@/features/media-operations/__tests__/fixtures'
import { api } from '@/lib/api'

import { getOperationsSnapshot } from '../api'

const originalAdapter = api.defaults.adapter
afterEach(() => {
  api.defaults.adapter = originalAdapter
})

it('aborts a concurrent sibling read when one half of an observation fails instead of leaving an overlapping request', async () => {
  let siblingSignal: AbortSignal | undefined
  api.defaults.adapter = (config) => {
    if (config.url === '/api/log/stat') {
      siblingSignal = config.signal as AbortSignal
      return new Promise(() => {})
    }
    return Promise.resolve({
      data: { success: false, message: 'Fixture observation unavailable' },
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    })
  }
  await expect(
    getOperationsSnapshot(
      'usage',
      { start_timestamp: 1, end_timestamp: 301 },
      new AbortController().signal
    )
  ).rejects.toThrow()
  expect(siblingSignal?.aborted).toBe(true)
})
it('preserves server total separately from the bounded error page and sends the exact read filters', async () => {
  api.defaults.adapter = async (config) => {
    expect(config.url).toBe('/api/log/')
    expect(config.params).toEqual({
      start_timestamp: 1,
      end_timestamp: 301,
      type: 5,
      p: 1,
      page_size: 20,
    })
    expect(config.method).toBe('get')
    expect(config.disableDuplicate).toBe(true)
    return {
      data: { success: true, data: { items: [], total: 420 } },
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    }
  }
  const snapshot = await getOperationsSnapshot(
    'errors',
    { start_timestamp: 1, end_timestamp: 301 },
    new AbortController().signal
  )
  expect(snapshot.tab).toBe('errors')
  if (snapshot.tab !== 'errors') throw new Error('Unexpected fixture tab')
  expect(snapshot.logs.total).toBe(420)
  expect(snapshot.logs.items).toEqual([])
})

it.each(['image', 'video'] as const)(
  'loads only %s tasks with the exact server-side category filter',
  async (kind) => {
    api.defaults.adapter = async (config) => {
      expect(config.url).toBe('/api/media-operations')
      expect(config.method).toBe('get')
      expect(config.params).toEqual({
        start_timestamp: 1,
        end_timestamp: 301,
        media_kind: kind,
      })
      return {
        data: { success: true, data: emptyMediaPayload },
        status: 200,
        statusText: 'OK',
        headers: {},
        config,
      }
    }
    const snapshot = await getOperationsSnapshot(
      kind,
      { start_timestamp: 1, end_timestamp: 301 },
      new AbortController().signal
    )
    expect(snapshot.tab).toBe(kind)
  }
)

it.each(['profit', 'usage', 'errors', 'image', 'video'] as const)(
  'sends a pasted model using the actual %s endpoint contract',
  async (tab) => {
    const requests: { url?: string; params: Record<string, unknown> }[] = []
    api.defaults.adapter = async (config) => {
      requests.push({ url: config.url, params: config.params })
      let data: unknown = { rows: [], summary: {} }
      if (config.url === '/api/log/stat') {
        data = { quota: 0, rpm: 0, tpm: 0 }
      } else if (tab === 'usage' || tab === 'errors') {
        data = { items: [], total: 0 }
      } else if (tab === 'image' || tab === 'video') {
        data = emptyMediaPayload
      }
      return {
        data: { success: true, data },
        status: 200,
        statusText: 'OK',
        headers: {},
        config,
      }
    }
    const window = { start_timestamp: 1, end_timestamp: 301 }
    await getOperationsSnapshot(
      tab,
      window,
      new AbortController().signal,
      '  deepseek-flash  '
    )
    expect(requests.length).toBe(tab === 'usage' ? 2 : 1)
    for (const request of requests) {
      const field = tab === 'image' || tab === 'video' ? 'model' : 'model_name'
      expect(request.params[field]).toBe('deepseek-flash')
      expect(request.params.start_timestamp).toBe(1)
      expect(request.params.end_timestamp).toBe(301)
    }
  }
)

it('blocks invalid periods and oversized model names before any API read', async () => {
  let reads = 0
  api.defaults.adapter = async () => {
    reads++
    throw new Error('Unexpected invalid read')
  }
  for (const [window, model] of [
    [{ start_timestamp: 301, end_timestamp: 1 }, 'valid-model'],
    [{ start_timestamp: 1, end_timestamp: 301 }, 'x'.repeat(256)],
    [{ start_timestamp: 1, end_timestamp: 301 }, 'invalid\u0000model'],
  ] as const) {
    await expect(
      getOperationsSnapshot(
        'errors',
        window,
        new AbortController().signal,
        model
      )
    ).rejects.toThrow('Invalid parameters')
  }
  expect(reads).toBe(0)
})

it.each(['concurrency', 'activity'] as const)(
  'does not send unsupported model filters for %s',
  async (tab) => {
    const params: Record<string, unknown>[] = []
    api.defaults.adapter = async (config) => {
      params.push(config.params ?? {})
      return {
        data: {
          success: true,
          data:
            tab === 'concurrency'
              ? {
                  schema_version: 1,
                  revision: 'test',
                  defaults: { video: 4, image: 10 },
                  pool_overrides: [],
                  pools: [],
                }
              : { items: [], total: 0 },
        },
        status: 200,
        statusText: 'OK',
        headers: {},
        config,
      }
    }
    await getOperationsSnapshot(
      tab,
      { start_timestamp: 1, end_timestamp: 301 },
      new AbortController().signal,
      'irrelevant-model'
    )
    for (const value of params) {
      expect(value).not.toHaveProperty('model_name')
      expect(value).not.toHaveProperty('model')
    }
  }
)
