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
import { useQuery } from '@tanstack/react-query'
import type { PaginationState, Updater } from '@tanstack/react-table'
import { useEffect, useState } from 'react'

import { getVideoUsage, VideoUsageTooBroadError } from '../api'
import type { VideoChannel, VideoProvider, VideoUser } from '../schema'
import { initialVideoFilters, videoQueryFromDraft } from './filters'

export function useVideoReport(userId: number, active = true) {
  const [draft, setDraft] = useState(initialVideoFilters)
  const [query, setQuery] = useState(() => {
    const initial = videoQueryFromDraft(draft)
    if (!initial) throw new Error('Invalid initial video usage period')
    return initial
  })
  const [options, setOptions] = useState<{
    models: string[]
    users: VideoUser[]
    channels: VideoChannel[]
    providers: VideoProvider[]
  }>({ models: [], users: [], channels: [], providers: [] })
  const parsed = videoQueryFromDraft(draft)
  const report = useQuery({
    queryKey: ['video-usage', userId, query],
    queryFn: ({ signal }) => getVideoUsage(query, signal),
    enabled: active,
    retry: false,
    meta: { errorToast: false },
  })
  const data = report.data
  useEffect(() => {
    if (!data) return
    setOptions((previous) => {
      const users = new Map(previous.users.map((user) => [user.id, user]))
      data.filter_options.users.forEach((user) => users.set(user.id, user))
      const channels = new Map(previous.channels.map((item) => [item.id, item]))
      data.filter_options.channels.forEach((item) =>
        channels.set(item.id, item)
      )
      return {
        channels: [...channels.values()].sort((a, b) => a.id - b.id),
        providers: [
          ...new Set([...previous.providers, ...data.filter_options.providers]),
        ].sort(),
        models: [
          ...new Set([...previous.models, ...data.filter_options.models]),
        ].sort(),
        users: [...users.values()].sort((a, b) => a.id - b.id),
      }
    })
  }, [data])
  return {
    draft,
    setDraft,
    query,
    report,
    parsed,
    models: [
      ...new Set(
        [
          ...options.models,
          ...(data?.filter_options.models ?? []),
          draft.model,
        ].filter(Boolean)
      ),
    ].sort(),
    users: options.users,
    channels: options.channels,
    providers: options.providers,
    tooBroad: report.error instanceof VideoUsageTooBroadError,
    apply: () => {
      if (parsed) {
        setQuery({
          ...parsed,
          task_scope: query.task_scope,
          page_size: query.page_size,
        })
      }
    },
    reset: () => {
      const next = initialVideoFilters()
      setDraft(next)
      const initial = videoQueryFromDraft(next)
      if (initial) setQuery(initial)
    },
    setRecentRange: (seconds: 300 | 3600) => {
      const end = new Date()
      const start = new Date(end.getTime() - seconds * 1000)
      setDraft((previous) => ({ ...previous, start, end }))
      setQuery((previous) => ({
        ...previous,
        start_timestamp: Math.floor(start.getTime() / 1000),
        end_timestamp: Math.floor(end.getTime() / 1000),
        p: 1,
      }))
    },
    setScope: (scope: 'history' | 'current') =>
      setQuery((previous) => ({ ...previous, task_scope: scope, p: 1 })),
    setPage: (update: Updater<PaginationState>) => {
      if (report.isFetching || report.isError) return
      setQuery((previous) => {
        const current = {
          pageIndex: previous.p - 1,
          pageSize: previous.page_size,
        }
        const next = typeof update === 'function' ? update(current) : update
        return {
          ...previous,
          p: next.pageSize === previous.page_size ? next.pageIndex + 1 : 1,
          page_size: next.pageSize,
        }
      })
    },
  }
}
