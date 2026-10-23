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
import { CancelledError, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useMemo, useRef, useState } from 'react'

import { useAuthStore } from '@/stores/auth-store'

import { getOperationsSnapshot } from '../api'
import {
  availableOperationsTabs,
  operationsScope,
  operationsWindow,
  supportsOperationsModelFilter,
  validOperationsFilters,
  type OperationsRange,
  type OperationsTab,
  type OperationsWindow,
} from '../policy'

export function useOperationsQuery(props: {
  scope: string
  tab: OperationsTab
  range: OperationsRange
  paused: boolean
  modelName?: string
  customWindow?: OperationsWindow
}) {
  const client = useQueryClient()
  const user = useAuthStore((state) => state.auth.user)
  const sid = useAuthStore((state) => state.auth.session?.sid)
  const authorized =
    operationsScope(user, sid) === props.scope &&
    availableOperationsTabs(user).includes(props.tab)
  const [visible, setVisible] = useState(
    () => document.visibilityState === 'visible'
  )
  const [online, setOnline] = useState(() => navigator.onLine)
  const [now, setNow] = useState(Date.now)
  const failures = useRef(0)
  const [queryWindow, setWindow] = useState<OperationsWindow>()
  const interval = props.tab === 'profit' ? 60_000 : 30_000
  const modelName = supportsOperationsModelFilter(props.tab)
    ? props.modelName?.trim() || undefined
    : undefined
  const customWindow =
    props.tab === 'concurrency' ? undefined : props.customWindow
  const start = customWindow?.start_timestamp
  const end = customWindow?.end_timestamp
  const invalid = !validOperationsFilters(
    props.tab,
    customWindow ?? operationsWindow(props.range),
    modelName
  )
  const running = authorized && !invalid && visible && online && !props.paused
  let windowIdentity: string = props.range
  if (start !== undefined) windowIdentity = 'custom'
  if (props.tab === 'concurrency') windowIdentity = 'live'
  const queryKey = useMemo(
    () =>
      [
        'operations-screen',
        props.scope,
        props.tab,
        windowIdentity,
        modelName ?? '',
        start ?? null,
        end ?? null,
      ] as const,
    [props.scope, props.tab, windowIdentity, modelName, start, end]
  )
  const query = useQuery({
    queryKey,
    queryFn: async ({ signal }) => {
      const state = useAuthStore.getState().auth
      if (
        operationsScope(state.user, state.session?.sid) !== props.scope ||
        !availableOperationsTabs(state.user).includes(props.tab)
      ) {
        throw new CancelledError()
      }
      try {
        const requestedWindow = customWindow ?? operationsWindow(props.range)
        setWindow(requestedWindow)
        const snapshot = await getOperationsSnapshot(
          props.tab,
          requestedWindow,
          signal,
          modelName
        )
        const current = useAuthStore.getState().auth
        if (
          signal.aborted ||
          operationsScope(current.user, current.session?.sid) !== props.scope
        ) {
          throw new CancelledError()
        }
        failures.current = 0
        return snapshot
      } catch (error) {
        if (!signal.aborted && !(error instanceof CancelledError)) {
          failures.current++
        }
        throw error
      }
    },
    enabled: running,
    retry: false,
    staleTime: Infinity,
    gcTime: 60_000,
    refetchOnMount: 'always',
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    meta: { errorToast: false },
  })
  const refetch = query.refetch
  useEffect(() => {
    const visibility = () => setVisible(document.visibilityState === 'visible')
    const connectivity = () => setOnline(navigator.onLine)
    document.addEventListener('visibilitychange', visibility)
    window.addEventListener('online', connectivity)
    window.addEventListener('offline', connectivity)
    const clock = window.setInterval(() => setNow(Date.now()), 5_000)
    return () => {
      document.removeEventListener('visibilitychange', visibility)
      window.removeEventListener('online', connectivity)
      window.removeEventListener('offline', connectivity)
      window.clearInterval(clock)
    }
  }, [])
  useEffect(() => {
    const key = queryKey
    const unsubscribe = useAuthStore.subscribe((state) => {
      if (
        operationsScope(state.auth.user, state.auth.session?.sid) !==
        props.scope
      ) {
        void client.cancelQueries({ queryKey: key, exact: true })
        client.removeQueries({ queryKey: key, exact: true })
      }
    })
    return () => {
      unsubscribe()
      void client.cancelQueries({ queryKey: key, exact: true })
      client.removeQueries({ queryKey: key, exact: true })
    }
  }, [client, props.scope, queryKey])
  useEffect(() => {
    if (
      !running ||
      query.isFetching ||
      (!query.dataUpdatedAt && !query.errorUpdatedAt)
    ) {
      return
    }
    const timer = window.setTimeout(
      () => void refetch({ cancelRefetch: false }),
      interval * 2 ** Math.min(failures.current, 3)
    )
    return () => window.clearTimeout(timer)
  }, [
    running,
    query.isFetching,
    query.dataUpdatedAt,
    query.errorUpdatedAt,
    interval,
    refetch,
  ])
  const refresh = () => {
    if (authorized && !invalid && visible && online && !query.isFetching) {
      void refetch({ cancelRefetch: false })
    }
  }
  useEffect(() => {
    if (!authorized) {
      const key = queryKey
      void client.cancelQueries({ queryKey: key, exact: true })
      client.removeQueries({ queryKey: key, exact: true })
    }
  }, [authorized, client, props.scope, queryKey])
  return {
    query,
    refresh,
    visible,
    online,
    running,
    interval,
    window: queryWindow,
    stale: query.dataUpdatedAt > 0 && now - query.dataUpdatedAt > interval * 2,
    authorized,
    invalid,
    modelName,
  }
}
