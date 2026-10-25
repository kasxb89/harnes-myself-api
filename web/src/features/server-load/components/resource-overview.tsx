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

import { Badge } from '@/components/ui/badge'
import { Progress } from '@/components/ui/progress'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { cn } from '@/lib/utils'

import type { ServerLoadSnapshot } from '../api'
import { formatServerBytes, formatServerPercent } from '../format'

export function ResourceOverview(props: { snapshot: ServerLoadSnapshot }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const unknown = t('Not collected')
  const snapshot = props.snapshot
  const metrics = [
    {
      key: 'cpu',
      label: t('Visible CPU usage'),
      value: snapshot.cpu.usage_percent,
      threshold: snapshot.thresholds.cpu_percent,
      detail: t('{{count}} logical cores', {
        count: snapshot.cpu.logical_cores,
        formatParams: { count: { locale } },
      }),
    },
    {
      key: 'memory',
      label: t('Visible system memory'),
      value: snapshot.memory.used_percent,
      threshold: snapshot.thresholds.memory_percent,
      detail: t('{{available}} available / {{total}} total', {
        available: formatServerBytes(
          snapshot.memory.available_bytes,
          locale,
          unknown
        ),
        total: formatServerBytes(snapshot.memory.total_bytes, locale, unknown),
      }),
    },
    {
      key: 'disk',
      label: t('Service storage space'),
      value: snapshot.disk.used_percent,
      threshold: snapshot.thresholds.disk_percent,
      detail: t('{{available}} available / {{total}} total', {
        available: formatServerBytes(
          snapshot.disk.available_bytes,
          locale,
          unknown
        ),
        total: formatServerBytes(snapshot.disk.total_bytes, locale, unknown),
      }),
    },
  ]
  return (
    <section
      className='grid grid-cols-1 gap-6 py-5 sm:grid-cols-3'
      aria-label={t('Resource overview')}
    >
      {metrics.map((metric) => {
        const high =
          snapshot.thresholds.guard_enabled &&
          metric.value !== null &&
          metric.threshold > 0 &&
          Math.trunc(metric.value) > metric.threshold
        return (
          <section
            key={metric.key}
            aria-label={metric.label}
            className='flex min-w-0 flex-col gap-2'
          >
            <div className='flex flex-wrap items-center gap-2'>
              <h3 className='text-muted-foreground text-sm'>{metric.label}</h3>
              {high && (
                <Badge variant='destructive'>
                  {t('Observed value exceeds protection threshold')}
                </Badge>
              )}
            </div>
            <p
              className={cn(
                'text-3xl font-semibold tracking-tight tabular-nums',
                high && 'text-destructive'
              )}
            >
              {formatServerPercent(metric.value, locale, unknown)}
            </p>
            {metric.value !== null && (
              <Progress value={metric.value} aria-label={metric.label} />
            )}
            <p className='text-muted-foreground text-xs break-words'>
              {metric.detail}
            </p>
            {metric.key === 'cpu' && (
              <p className='text-muted-foreground text-xs'>
                {t('Load averages (1 / 5 / 15 min)')}:{' '}
                {[
                  snapshot.cpu.load_1,
                  snapshot.cpu.load_5,
                  snapshot.cpu.load_15,
                ]
                  .map((value) =>
                    value === null ? unknown : formatNumber(value, locale)
                  )
                  .join(' / ')}
              </p>
            )}
          </section>
        )
      })}
    </section>
  )
}
