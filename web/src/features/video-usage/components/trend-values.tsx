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
import { useTranslation } from 'react-i18next'

import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'

import { useVideoFormat } from '../lib/format'
import type { VideoIngressPoint, VideoTrendPoint } from '../schema'

export function VideoTrendValues(props: {
  points: VideoTrendPoint[]
  ingress: VideoIngressPoint[]
}) {
  const { t } = useTranslation()
  const format = useVideoFormat()
  const windowColumn = {
    id: 'window',
    header: t('Bucket interval'),
    className: 'min-w-64',
    cell: (row: VideoTrendPoint | VideoIngressPoint) => (
      <div className='text-xs whitespace-normal'>
        [{format.time(row.start_timestamp)}, {format.time(row.end_timestamp)})
      </div>
    ),
  }
  const taskColumns: StaticDataTableColumn<VideoTrendPoint>[] = [
    windowColumn,
    {
      id: 'seconds',
      header: t('Actual bucket seconds'),
      cell: (row) => format.count(row.seconds),
    },
    {
      id: 'tasks',
      header: t('Accepted tasks'),
      cell: (row) => format.count(row.accepted_tasks),
    },
    {
      id: 'rate',
      header: t('Accepted tasks / s'),
      cell: (row) => format.rate(row.accepted_task_rate),
    },
    {
      id: 'peak',
      header: t('Recorded in-flight peak'),
      cell: (row) =>
        row.recorded_peak === null
          ? t('Unknown')
          : format.count(row.recorded_peak),
    },
    {
      id: 'exclusions',
      header: t('Peak interval exclusions'),
      className: 'min-w-64',
      cell: (row) => (
        <span className='text-xs whitespace-normal'>
          {format.exclusions(row.peak_exclusions)}
        </span>
      ),
    },
  ]
  const count = (value: number | null) =>
    value === null ? t('Unknown') : format.count(value)
  const coverageLabels = {
    complete: t('Complete coverage'),
    partial: t('Partial coverage'),
    unavailable: t('Not observed'),
  }
  const ingressColumns: StaticDataTableColumn<VideoIngressPoint>[] = [
    windowColumn,
    {
      id: 'coverage',
      header: t('Ingress coverage'),
      cell: (row) => (
        <div className='space-y-1 text-xs'>
          <div>{coverageLabels[row.coverage]}</div>
          <div>
            {format.count(row.covered_seconds)} / {format.count(row.seconds)} s
          </div>
        </div>
      ),
    },
    {
      id: 'submit',
      header: t('Submit POST requests / QPS'),
      cell: (row) => (
        <div>
          {count(row.submit_requests)} / {format.rate(row.submit_qps)}
        </div>
      ),
    },
    {
      id: 'status',
      header: t('Status GET requests / QPS'),
      cell: (row) => (
        <div>
          {count(row.status_requests)} / {format.rate(row.status_qps)}
        </div>
      ),
    },
    {
      id: 'pending',
      header: t('Submit pending'),
      cell: (row) => count(row.submit_pending),
    },
    {
      id: 'accepted',
      header: t('Durable acceptances'),
      cell: (row) => count(row.submit_accepted),
    },
    {
      id: 'ended',
      header: t('Ended without acceptance'),
      cell: (row) => count(row.submit_ended_without_acceptance),
    },
  ]
  return (
    <details className='min-w-0 rounded-xl border p-4'>
      <summary className='cursor-pointer text-sm font-medium'>
        {t('View bucket values')}
      </summary>
      <div className='mt-4 min-w-0 space-y-4'>
        <StaticDataTable
          columns={taskColumns}
          data={props.points}
          getRowKey={(row) => row.start_timestamp}
          tableProps={{ 'aria-label': t('Task trend bucket values') }}
        />
        <StaticDataTable
          columns={ingressColumns}
          data={props.ingress}
          getRowKey={(row) => row.start_timestamp}
          tableProps={{ 'aria-label': t('Ingress bucket values') }}
        />
        <p className='text-muted-foreground text-xs'>
          {t(
            'Ended without acceptance is not proof that an upstream request was never sent.'
          )}
        </p>
      </div>
    </details>
  )
}
