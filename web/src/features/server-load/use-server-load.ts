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

import { getServerLoad, type ServerLoadSnapshot } from './api'
import { canViewServerLoad, serverLoadScope } from './policy'
import { appendServerSample } from './trend'

const INTERVAL = 10_000

export function useServerLoad(props: { scope: string; paused: boolean }) {
  const client = useQueryClient()
  const user = useAuthStore((state) => state.auth.user)
  const sid = useAuthStore((state) => state.auth.session?.sid)
  const authorized =
    canViewServerLoad(user) && serverLoadScope(user, sid) === props.scope
  const [visible, setVisible] = useState(
    () => document.visibilityState === 'visible'
  )
  const [online, setOnline] = useState(() => navigator.onLine)
  const [now, setNow] = useState(Date.now)
  const [samples, setSamples] = useState<ServerLoadSnapshot[]>([])
  const failures = useRef(0)
  const running = authorized && visible && online && !props.paused
  const queryKey = useMemo(
    () => ['server-load', props.scope] as const,
    [props.scope]
  )
  const query = useQuery({
    queryKey,
    queryFn: async ({ signal }) => {
      const auth = useAuthStore.getState().auth
      if (
        !canViewServerLoad(auth.user) ||
        serverLoadScope(auth.user, auth.session?.sid) !== props.scope
      ) {
        throw new CancelledError()
      }
      try {
        const snapshot = await getServerLoad(signal)
        const current = useAuthStore.getState().auth
        if (
          signal.aborted ||
          serverLoadScope(current.user, current.session?.sid) !== props.scope
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
    gcTime: 0,
    refetchOnMount: 'always',
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    meta: { errorToast: false },
  })
  const refetch = query.refetch
  useEffect(() => {
    const snapshot = query.data
    if (snapshot && authorized) {
      setSamples((previous) => appendServerSample(previous, snapshot))
    }
  }, [query.data, authorized])
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
    const unsubscribe = useAuthStore.subscribe((state) => {
      if (
        serverLoadScope(state.auth.user, state.auth.session?.sid) !==
        props.scope
      ) {
        void client.cancelQueries({ queryKey, exact: true })
        client.removeQueries({ queryKey, exact: true })
        setSamples([])
      }
    })
    return () => {
      unsubscribe()
      void client.cancelQueries({ queryKey, exact: true })
      client.removeQueries({ queryKey, exact: true })
    }
  }, [client, props.scope, queryKey])
  useEffect(() => {
    if (!running) void client.cancelQueries({ queryKey, exact: true })
  }, [running, client, queryKey])
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
      INTERVAL * 2 ** Math.min(failures.current, 3)
    )
    return () => window.clearTimeout(timer)
  }, [
    running,
    query.isFetching,
    query.dataUpdatedAt,
    query.errorUpdatedAt,
    refetch,
  ])
  const refresh = () => {
    if (authorized && visible && online && !query.isFetching) {
      void refetch({ cancelRefetch: false })
    }
  }
  return {
    query,
    refresh,
    running,
    visible,
    online,
    authorized,
    samples: authorized ? samples : [],
    stale: !!query.data && now - query.data.sampled_at * 1000 > INTERVAL * 2,
  }
}
