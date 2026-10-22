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
import type { ColumnDef } from '@tanstack/react-table'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'

import { videoStatusLabelKey } from '../labels'
import { useVideoFormat, userOptionLabel } from '../lib/format'
import type { VideoTask } from '../schema'
import { VideoTimingBadge } from './timing-badge'

export function useVideoTaskColumns(): ColumnDef<VideoTask>[] {
  const { t } = useTranslation()
  const format = useVideoFormat()
  return useMemo(
    () => [
      {
        id: 'record',
        header: t('Record / task ID'),
        size: 200,
        cell: ({ row }) => (
          <div className='max-w-52 space-y-1 whitespace-normal'>
            <span>#{row.original.id}</span>
            <div className='font-mono text-xs break-all'>
              {row.original.task_id || t('Unknown')}
            </div>
          </div>
        ),
      },
      {
        id: 'model',
        header: t('Model'),
        size: 210,
        meta: { mobileTitle: true },
        cell: ({ row }) => (
          <span className='font-mono text-xs break-all whitespace-normal'>
            {row.original.model || t('Unknown model')}
          </span>
        ),
      },
      {
        id: 'user',
        header: t('User'),
        size: 160,
        cell: ({ row }) => (
          <span className='break-words whitespace-normal'>
            {row.original.user
              ? userOptionLabel(row.original.user)
              : t('Unknown')}
          </span>
        ),
      },
      {
        id: 'source',
        header: t('Client source'),
        size: 190,
        cell: ({ row }) => (
          <span className='whitespace-normal'>
            {row.original.client_source === 'desktop'
              ? t('Desktop client declaration')
              : t('Unknown')}
          </span>
        ),
      },
      {
        id: 'status',
        header: t('Status'),
        size: 120,
        meta: { mobileBadge: true },
        cell: ({ row }) => (
          <Badge variant='outline' className='h-auto min-h-5 whitespace-normal'>
            {t(videoStatusLabelKey(row.original.status))}
          </Badge>
        ),
      },
      {
        id: 'times',
        header: t('Recorded submit / start / finish'),
        size: 265,
        cell: ({ row }) => (
          <div className='min-w-44 space-y-1 text-xs whitespace-normal'>
            <div>
              {t('Submission')}: {format.time(row.original.submitted_at)}
            </div>
            <div>
              {t('Observed start')}: {format.time(row.original.started_at)}
            </div>
            <div>
              {t('Observed finish')}: {format.time(row.original.finished_at)}
            </div>
            <VideoTimingBadge quality={row.original.timing_quality} />
          </div>
        ),
      },
      {
        id: 'age-wait',
        header: t('Recorded age / waiting'),
        size: 200,
        cell: ({ row }) => (
          <div className='space-y-1 text-xs whitespace-normal'>
            <div>
              {t('Age')}: {format.duration(row.original.age_seconds)}
            </div>
            <div>
              {t('Waiting')}: {format.duration(row.original.waiting_seconds)}
            </div>
          </div>
        ),
      },
      {
        id: 'turnaround',
        header: t('Terminal turnaround'),
        size: 160,
        cell: ({ row }) => format.duration(row.original.turnaround_seconds),
      },
      {
        id: 'running',
        header: t('Recorded running duration'),
        size: 180,
        cell: ({ row }) =>
          format.duration(row.original.recorded_running_seconds),
      },
      {
        id: 'video',
        header: t('Reported video duration'),
        size: 205,
        cell: ({ row }) => (
          <div className='space-y-1 text-xs whitespace-normal'>
            <div>{format.duration(row.original.reported_video_seconds)}</div>
            {row.original.reported_video_duration_source === 'h3_usage' && (
              <span className='text-muted-foreground'>
                {t('Upstream H3 usage report')}
              </span>
            )}
          </div>
        ),
      },
    ],
    [format, t]
  )
}
