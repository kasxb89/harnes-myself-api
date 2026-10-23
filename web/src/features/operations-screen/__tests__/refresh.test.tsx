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
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { useOperationsQuery } from '../hooks/use-operations-query'
import { operationsScope, type OperationsWindow } from '../policy'

const snapshot = {
  schema_version: 1,
  revision: 'revision',
  defaults: { video: 2, image: 3 },
  pool_overrides: [],
  pools: [],
}
const clients: QueryClient[] = []
const originalAdapter = api.defaults.adapter
let visible = true
let online = true

function setup(tab: 'concurrency' | 'errors' = 'concurrency') {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  const auth = useAuthStore.getState().auth
  const scope = operationsScope(auth.user, auth.session?.sid)
  const wrapper = (props: { children: React.ReactNode }) => (
    <QueryClientProvider client={client}>{props.children}</QueryClientProvider>
  )
  const hook = renderHook(
    (props: { tab: 'concurrency' | 'errors'; paused: boolean }) =>
      useOperationsQuery({
        scope,
        tab: props.tab,
        range: '5m',
        paused: props.paused,
      }),
    { wrapper, initialProps: { tab, paused: false } }
  )
  return { ...hook, client }
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
    permissions: { admin_permissions: { media_operations: { read: true } } },
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

describe('operations screen read-only refresh', () => {
  it('refreshes only the selected authorized tab, advances its window and keeps a bounded cache', async () => {
    const requests: string[] = []
    api.defaults.adapter = async (config) => {
      requests.push(config.url ?? '')
      return {
        data: { success: true, data: snapshot },
        status: 200,
        statusText: 'OK',
        headers: {},
        config,
      }
    }
    const hook = setup()
    await flush()
    expect(hook.result.current.query.isSuccess).toBe(true)
    expect(requests).toEqual(['/api/media-concurrency'])
    const firstEnd = hook.result.current.query.data?.window.end_timestamp
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000)
    })
    await flush()
    expect(requests).toHaveLength(2)
    expect(hook.result.current.query.data?.window.end_timestamp).toBe(
      (firstEnd ?? 0) + 30
    )
    expect(hook.client.getQueryCache().getAll()).toHaveLength(1)
    hook.rerender({ tab: 'errors', paused: false })
    await flush()
    expect(requests).toHaveLength(2)
  })
  it('pauses while hidden, offline or manually paused', async () => {
    let calls = 0
    api.defaults.adapter = async (config) => {
      calls++
      return {
        data: { success: true, data: snapshot },
        status: 200,
        statusText: 'OK',
        headers: {},
        config,
      }
    }
    const hook = setup()
    await flush()
    hook.rerender({ tab: 'concurrency', paused: true })
    await act(async () => {
      await vi.advanceTimersByTimeAsync(90_000)
    })
    expect(calls).toBe(1)
    visible = false
    act(() => document.dispatchEvent(new Event('visibilitychange')))
    hook.rerender({ tab: 'concurrency', paused: false })
    await act(async () => {
      await vi.advanceTimersByTimeAsync(90_000)
    })
    expect(calls).toBe(1)
    visible = true
    online = false
    act(() => {
      document.dispatchEvent(new Event('visibilitychange'))
      window.dispatchEvent(new Event('offline'))
    })
    await act(async () => {
      await vi.advanceTimersByTimeAsync(90_000)
    })
    expect(calls).toBe(1)
    online = true
    act(() => window.dispatchEvent(new Event('online')))
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000)
    })
    await flush()
    expect(calls).toBe(2)
  })
  it('aborts and removes old data on account change and ignores an adapter that completes late', async () => {
    let resolveResponse: (() => void) | undefined
    let signal: AbortSignal | undefined
    api.defaults.adapter = (config) =>
      new Promise((resolve) => {
        signal = config.signal as AbortSignal
        resolveResponse = () =>
          resolve({
            data: { success: true, data: snapshot },
            status: 200,
            statusText: 'OK',
            headers: {},
            config,
          })
      })
    const hook = setup()
    await flush()
    act(() =>
      useAuthStore
        .getState()
        .auth.setUser({ id: 8, username: 'other', role: 100 })
    )
    await flush()
    expect(signal?.aborted).toBe(true)
    await act(async () => {
      resolveResponse?.()
      await Promise.resolve()
    })
    await flush()
    expect(hook.result.current.query.data).toBeUndefined()
    expect(hook.client.getQueryCache().getAll()).toHaveLength(0)
  })
  it('does not overlap slow requests or manual refreshes and cancels on permission revocation', async () => {
    let calls = 0
    let signal: AbortSignal | undefined
    api.defaults.adapter = (config) =>
      new Promise(() => {
        calls++
        signal = config.signal as AbortSignal
      })
    const hook = setup()
    await flush()
    expect(hook.result.current.window).toEqual({
      start_timestamp: 1792810500,
      end_timestamp: 1792810800,
    })
    act(() => hook.result.current.refresh())
    await act(async () => {
      await vi.advanceTimersByTimeAsync(120_000)
    })
    expect(calls).toBe(1)
    act(() =>
      useAuthStore.getState().auth.setUser({
        id: 7,
        username: 'operator',
        role: 10,
        permissions: { admin_permissions: {} },
      })
    )
    await flush()
    expect(signal?.aborted).toBe(true)
    expect(hook.client.getQueryCache().getAll()).toHaveLength(0)
  })
  it('marks an old paused observation stale without replacing its last successful update', async () => {
    api.defaults.adapter = async (config) => ({
      data: { success: true, data: snapshot },
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    })
    const hook = setup()
    await flush()
    const updatedAt = hook.result.current.query.dataUpdatedAt
    hook.rerender({ tab: 'concurrency', paused: true })
    await act(async () => {
      await vi.advanceTimersByTimeAsync(65_000)
    })
    expect(hook.result.current.stale).toBe(true)
    expect(hook.result.current.query.dataUpdatedAt).toBe(updatedAt)
  })
  it('aborts an old request when the same account starts a different authenticated session', async () => {
    const auth = useAuthStore.getState().auth
    if (!auth.user) throw new Error('Missing fixture user')
    const session = {
      sid: 'first-session',
      current: true,
      login_method: 'fixture',
      ip: '',
      user_agent: '',
      created_at: 1,
      last_active_at: 1,
      expires_at: 1792897200,
    }
    const bundle = {
      user: auth.user,
      access_token: 'fixture-token',
      token_type: 'Bearer',
      access_expires_at: 1792897200,
      session,
    }
    auth.setBundle(bundle)
    let signal: AbortSignal | undefined
    api.defaults.adapter = (config) =>
      new Promise(() => {
        signal = config.signal as AbortSignal
      })
    const hook = setup()
    await flush()
    act(() =>
      auth.setBundle({
        ...bundle,
        session: { ...session, sid: 'second-session' },
      })
    )
    await flush()
    expect(signal?.aborted).toBe(true)
    expect(hook.client.getQueryCache().getAll()).toHaveLength(0)
  })
  it('backs off failed automatic refreshes while preserving the last successful sample', async () => {
    let calls = 0
    api.defaults.adapter = async (config) => {
      calls++
      if (calls > 1) throw new Error('fixture unavailable')
      return {
        data: { success: true, data: snapshot },
        status: 200,
        statusText: 'OK',
        headers: {},
        config,
      }
    }
    const hook = setup()
    await flush()
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000)
    })
    await flush()
    expect(hook.result.current.query.isError).toBe(true)
    expect(hook.result.current.query.data).toBeDefined()
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000)
    })
    expect(calls).toBe(2)
    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000)
    })
    await flush()
    expect(calls).toBe(3)
  })
})

it('isolates typed model and custom time filters, cancels late old reads, and blocks invalid replacement ranges', async () => {
  useAuthStore.getState().auth.setUser({
    id: 7,
    username: 'operator',
    role: 10,
    permissions: { admin_permissions: { diagnostics: { read: true } } },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  const scope = operationsScope(useAuthStore.getState().auth.user, undefined)
  const requests: { model: string; end: number; signal: AbortSignal }[] = []
  let finishOld: (() => void) | undefined
  api.defaults.adapter = async (config) => {
    requests.push({
      model: config.params.model_name,
      end: config.params.end_timestamp,
      signal: config.signal as AbortSignal,
    })
    const response = {
      data: {
        success: true,
        data: {
          items: [],
          total: config.params.model_name === 'old-model' ? 9 : 2,
        },
      },
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    }
    if (config.params.model_name === 'old-model') {
      return new Promise((resolve) => {
        finishOld = () => resolve(response)
      })
    }
    return response
  }
  const initialWindow = { start_timestamp: 100, end_timestamp: 200 }
  const hook = renderHook(
    (props: { modelName: string; customWindow: OperationsWindow }) =>
      useOperationsQuery({
        scope,
        tab: 'errors',
        range: '5m',
        paused: false,
        ...props,
      }),
    {
      initialProps: { modelName: 'old-model', customWindow: initialWindow },
      wrapper: (props) => (
        <QueryClientProvider client={client}>
          {props.children}
        </QueryClientProvider>
      ),
    }
  )
  await flush()
  hook.rerender({
    modelName: 'new-model',
    customWindow: { start_timestamp: 100, end_timestamp: 300 },
  })
  await flush()
  expect(requests[0].signal.aborted).toBe(true)
  expect(hook.result.current.query.data?.tab).toBe('errors')
  expect(hook.result.current.query.data?.window.end_timestamp).toBe(300)
  await act(async () => {
    finishOld?.()
    await Promise.resolve()
  })
  await flush()
  const current = hook.result.current.query.data
  expect(current?.tab === 'errors' ? current.logs.total : undefined).toBe(2)
  expect(client.getQueryCache().getAll()).toHaveLength(1)
  await act(async () => {
    await vi.advanceTimersByTimeAsync(30_000)
  })
  await flush()
  expect(requests.at(-1)?.model).toBe('new-model')
  expect(requests.at(-1)?.end).toBe(300)
  const reads = requests.length
  hook.rerender({
    modelName: 'new-model',
    customWindow: { start_timestamp: 400, end_timestamp: 300 },
  })
  await flush()
  act(() => hook.result.current.refresh())
  await flush()
  expect(hook.result.current.invalid).toBe(true)
  expect(hook.result.current.query.data).toBeUndefined()
  expect(requests).toHaveLength(reads)
})
