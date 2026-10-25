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
import type { ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber, formatTimestampToDate } from '@/lib/format'

import type { DatabasePool, ServerLoadSnapshot } from '../api'
import { formatServerBytes, formatServerPercent } from '../format'

function DetailList(props: { title: string; rows: [string, ReactNode][] }) {
  return (
    <section className='min-w-0 py-5' aria-label={props.title}>
      <h3 className='mb-3 font-medium'>{props.title}</h3>
      <dl className='grid grid-cols-[minmax(0,1fr)_minmax(0,1fr)] gap-x-3 gap-y-3 text-sm'>
        {props.rows.map(([label, value]) => (
          <div key={label} className='contents'>
            <dt className='text-muted-foreground'>{label}</dt>
            <dd className='text-right break-words tabular-nums'>{value}</dd>
          </div>
        ))}
      </dl>
    </section>
  )
}

export function LoadDetails(props: { snapshot: ServerLoadSnapshot }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const snapshot = props.snapshot
  const unknown = t('Not collected')
  const bytes = (value: number | null) =>
    formatServerBytes(value, locale, unknown)
  const number = (value: number | null) =>
    value === null ? unknown : formatNumber(value, locale)
  const limit = (
    status: 'unknown' | 'unlimited' | 'limited',
    value: string
  ) => {
    if (status === 'unknown') return unknown
    if (status === 'unlimited') return t('No visible cgroup limit')
    return value
  }
  const poolStatus = (pool: DatabasePool) => {
    if (pool.status === 'up') {
      return <Badge variant='secondary'>{t('Connected')}</Badge>
    }
    if (pool.status === 'down') {
      return <Badge variant='destructive'>{t('Unavailable')}</Badge>
    }
    return unknown
  }
  const databaseRows: [string, ReactNode, ReactNode][] = [
    [
      t('Status'),
      poolStatus(snapshot.database.main),
      poolStatus(snapshot.database.logs),
    ],
    [
      t('Open connections'),
      number(snapshot.database.main.open_connections),
      number(snapshot.database.logs.open_connections),
    ],
    [
      t('In-use connections'),
      number(snapshot.database.main.in_use),
      number(snapshot.database.logs.in_use),
    ],
    [
      t('Idle connections'),
      number(snapshot.database.main.idle),
      number(snapshot.database.logs.idle),
    ],
    [
      t('Maximum open connections'),
      snapshot.database.main.max_open_connections === 0
        ? t('No configured pool limit')
        : number(snapshot.database.main.max_open_connections),
      snapshot.database.logs.max_open_connections === 0
        ? t('No configured pool limit')
        : number(snapshot.database.logs.max_open_connections),
    ],
    [
      t('Connection waits (cumulative)'),
      number(snapshot.database.main.wait_count),
      number(snapshot.database.logs.wait_count),
    ],
    [
      t('Connection wait time (cumulative)'),
      snapshot.database.main.wait_duration_ms === null
        ? unknown
        : `${number(snapshot.database.main.wait_duration_ms)} ms`,
      snapshot.database.logs.wait_duration_ms === null
        ? unknown
        : `${number(snapshot.database.logs.wait_duration_ms)} ms`,
    ],
  ]
  return (
    <div className='grid grid-cols-1 gap-x-10 lg:grid-cols-2'>
      <DetailList
        title={t('API process')}
        rows={[
          [t('Resident memory (RSS)'), bytes(snapshot.process.rss_bytes)],
          [t('Go heap in use'), bytes(snapshot.process.go_heap_alloc_bytes)],
          [t('Go heap reserved'), bytes(snapshot.process.go_heap_sys_bytes)],
          [
            t('Go runtime allocated memory'),
            bytes(snapshot.process.go_sys_bytes),
          ],
          [t('Goroutines'), number(snapshot.process.goroutines)],
          [t('GC cycles'), number(snapshot.process.gc_cycles)],
          [t('Started'), formatTimestampToDate(snapshot.service.started_at)],
          [
            t('Uptime'),
            t('{{count}} seconds', {
              count: snapshot.service.uptime_seconds,
              formatParams: { count: { locale } },
            }),
          ],
          [t('Version'), snapshot.service.version],
          [t('Runtime'), snapshot.runtime_os],
        ]}
      />
      <section
        className='min-w-0 py-5'
        aria-label={t('Database connection pools')}
      >
        <div className='mb-2 flex flex-wrap items-center gap-2'>
          <h3 className='font-medium'>{t('Database connection pools')}</h3>
          {snapshot.database.shared && (
            <Badge variant='outline'>{t('Shared pool')}</Badge>
          )}
        </div>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Metric')}</TableHead>
              <TableHead className='text-right'>{t('Main database')}</TableHead>
              <TableHead className='text-right'>{t('Log database')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {databaseRows.map(([label, main, logs]) => (
              <TableRow key={label}>
                <TableCell className='text-muted-foreground whitespace-normal'>
                  {label}
                </TableCell>
                <TableCell className='text-right tabular-nums'>
                  {main}
                </TableCell>
                <TableCell className='text-right tabular-nums'>
                  {logs}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </section>
      <DetailList
        title={t('Container resources')}
        rows={[
          [
            t('Environment'),
            snapshot.container.detected ? t('Container') : t('Host system'),
          ],
          [
            t('cgroup version'),
            snapshot.container.cgroup_version ??
              (snapshot.container.detected ? unknown : t('Not applicable')),
          ],
          [
            t('Container memory in use'),
            bytes(snapshot.container.memory_current_bytes),
          ],
          [
            t('Container memory limit'),
            limit(
              snapshot.container.memory_limit_status,
              bytes(snapshot.container.memory_limit_bytes)
            ),
          ],
          [
            t('CPU quota limit'),
            limit(
              snapshot.container.cpu_limit_status,
              snapshot.container.cpu_limit_cores === null
                ? unknown
                : t('{{count}} cores', {
                    count: snapshot.container.cpu_limit_cores,
                    formatParams: { count: { locale } },
                  })
            ),
          ],
        ]}
      />
      <DetailList
        title={t('Resource protection')}
        rows={[
          [
            t('Protection status'),
            snapshot.thresholds.guard_enabled ? (
              <Badge key='enabled' variant='secondary'>
                {t('Enabled')}
              </Badge>
            ) : (
              <Badge key='disabled' variant='outline'>
                {t('Disabled')}
              </Badge>
            ),
          ],
          [
            t('CPU threshold'),
            snapshot.thresholds.cpu_percent === 0
              ? t('Disabled')
              : formatServerPercent(
                  snapshot.thresholds.cpu_percent,
                  locale,
                  unknown
                ),
          ],
          [
            t('Memory threshold'),
            snapshot.thresholds.memory_percent === 0
              ? t('Disabled')
              : formatServerPercent(
                  snapshot.thresholds.memory_percent,
                  locale,
                  unknown
                ),
          ],
          [
            t('Storage threshold'),
            snapshot.thresholds.disk_percent === 0
              ? t('Disabled')
              : formatServerPercent(
                  snapshot.thresholds.disk_percent,
                  locale,
                  unknown
                ),
          ],
          [t('Service storage used'), bytes(snapshot.disk.used_bytes)],
        ]}
      />
    </div>
  )
}
