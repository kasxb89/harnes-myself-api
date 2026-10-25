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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, renderHook } from '@testing-library/react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { serverLoadScope } from '../policy'
import { useServerLoad } from '../use-server-load'
import { serverLoadFixture } from './fixtures'

const originalAdapter = api.defaults.adapter
const clients: QueryClient[] = []
let visible = true
let online = true

function setup() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  const auth = useAuthStore.getState().auth
  const scope = serverLoadScope(auth.user, auth.session?.sid)
  const wrapper = (props: { children: React.ReactNode }) => (
    <QueryClientProvider client={client}>{props.children}</QueryClientProvider>
  )
  return {
    ...renderHook(
      (props: { paused: boolean }) =>
        useServerLoad({ scope, paused: props.paused }),
      { wrapper, initialProps: { paused: false } }
    ),
    client,
  }
}
async function flush() {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(0)
  })
}
beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(new Date('2026-10-24T03:00:00Z'))
  visible = true
  online = true
  vi.spyOn(document, 'visibilityState', 'get').mockImplementation(() =>
    visible ? 'visible' : 'hidden'
  )
  vi.spyOn(navigator, 'onLine', 'get').mockImplementation(() => online)
  useAuthStore.getState().auth.setUser({
    id: 7,
    username: 'operator',
    role: 10,
    permissions: { admin_permissions: { diagnostics: { read: true } } },
  })
})
afterEach(() => {
  cleanup()
  clients.splice(0).forEach((client) => client.clear())
  api.defaults.adapter = originalAdapter
  useAuthStore.getState().auth.reset()
  vi.useRealTimers()
  vi.restoreAllMocks()
})
it('polls every ten seconds, retains only sixty page-local observations and removes them on unmount', async () => {
  let calls = 0
  api.defaults.adapter = async (config) => {
    calls++
    return {
      data: {
        success: true,
        data: serverLoadFixture(Math.floor(Date.now() / 1000)),
      },
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    }
  }
  const hook = setup()
  await flush()
  expect(hook.result.current.samples).toHaveLength(1)
  for (let index = 0; index < 70; index++) {
    await act(async () => {
      await vi.advanceTimersByTimeAsync(10_000)
    })
    await flush()
  }
  expect(calls).toBe(71)
  expect(hook.result.current.samples).toHaveLength(60)
  expect(hook.client.getQueryCache().getAll()).toHaveLength(1)
  hook.unmount()
  expect(hook.client.getQueryCache().getAll()).toHaveLength(0)
})
it('pauses when requested, hidden or offline, and allows a single manual sample while paused', async () => {
  let calls = 0
  api.defaults.adapter = async (config) => {
    calls++
    return {
      data: {
        success: true,
        data: serverLoadFixture(Math.floor(Date.now() / 1000)),
      },
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    }
  }
  const hook = setup()
  await flush()
  hook.rerender({ paused: true })
  await act(async () => {
    await vi.advanceTimersByTimeAsync(60_000)
  })
  expect(calls).toBe(1)
  expect(hook.result.current.stale).toBe(true)
  act(() => hook.result.current.refresh())
  await flush()
  expect(calls).toBe(2)
  visible = false
  act(() => document.dispatchEvent(new Event('visibilitychange')))
  hook.rerender({ paused: false })
  await act(async () => {
    await vi.advanceTimersByTimeAsync(60_000)
  })
  expect(calls).toBe(2)
  visible = true
  online = false
  act(() => {
    document.dispatchEvent(new Event('visibilitychange'))
    window.dispatchEvent(new Event('offline'))
  })
  await act(async () => {
    await vi.advanceTimersByTimeAsync(60_000)
  })
  expect(calls).toBe(2)
  online = true
  act(() => window.dispatchEvent(new Event('online')))
  await act(async () => {
    await vi.advanceTimersByTimeAsync(10_000)
  })
  await flush()
  expect(calls).toBe(3)
})
it('never overlaps a slow read, cancels it on pause and discards its late completion', async () => {
  let calls = 0
  let signal: AbortSignal | undefined
  let finish: (() => void) | undefined
  api.defaults.adapter = (config) =>
    new Promise((resolve) => {
      calls++
      signal = config.signal as AbortSignal
      finish = () =>
        resolve({
          data: { success: true, data: serverLoadFixture() },
          status: 200,
          statusText: 'OK',
          headers: {},
          config,
        })
    })
  const hook = setup()
  await flush()
  act(() => hook.result.current.refresh())
  await act(async () => {
    await vi.advanceTimersByTimeAsync(60_000)
  })
  expect(calls).toBe(1)
  hook.rerender({ paused: true })
  await flush()
  expect(signal?.aborted).toBe(true)
  await act(async () => {
    finish?.()
    await Promise.resolve()
  })
  await flush()
  expect(hook.result.current.samples).toEqual([])
})
it('backs off failed sampling without clearing the previous valid observation', async () => {
  let calls = 0
  api.defaults.adapter = async (config) => {
    calls++
    return {
      data:
        calls === 1
          ? {
              success: true,
              data: serverLoadFixture(Math.floor(Date.now() / 1000)),
            }
          : { success: false, message: 'Sampling unavailable' },
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    }
  }
  const hook = setup()
  await flush()
  await act(async () => {
    await vi.advanceTimersByTimeAsync(10_000)
  })
  await flush()
  expect(calls).toBe(2)
  expect(hook.result.current.query.isError).toBe(true)
  expect(hook.result.current.samples).toHaveLength(1)
  await act(async () => {
    await vi.advanceTimersByTimeAsync(10_000)
  })
  expect(calls).toBe(2)
  await act(async () => {
    await vi.advanceTimersByTimeAsync(10_000)
  })
  await flush()
  expect(calls).toBe(3)
  expect(hook.result.current.stale).toBe(true)
})
it('cancels and clears cached observations immediately when diagnostic access is revoked', async () => {
  let signal: AbortSignal | undefined
  api.defaults.adapter = (config) => {
    signal = config.signal as AbortSignal
    return new Promise(() => {})
  }
  const hook = setup()
  await flush()
  act(() =>
    useAuthStore
      .getState()
      .auth.setUser({ id: 7, username: 'operator', role: 10 })
  )
  await flush()
  expect(signal?.aborted).toBe(true)
  expect(hook.result.current.authorized).toBe(false)
  expect(hook.result.current.samples).toEqual([])
  expect(
    hook.client
      .getQueryCache()
      .getAll()
      .every((query) => query.state.data === undefined)
  ).toBe(true)
})
it('makes no diagnostic request for a normal account carrying a forged grant', async () => {
  useAuthStore.getState().auth.setUser({
    id: 8,
    username: 'user',
    role: 1,
    permissions: { admin_permissions: { diagnostics: { read: true } } },
  })
  let calls = 0
  api.defaults.adapter = async () => {
    calls++
    throw new Error('Unexpected request')
  }
  const hook = setup()
  await flush()
  expect(hook.result.current.authorized).toBe(false)
  expect(calls).toBe(0)
})
