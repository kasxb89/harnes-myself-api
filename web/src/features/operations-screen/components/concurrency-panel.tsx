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
import { CircleHelp, Gauge, Layers, Radio } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { Badge } from '@/components/ui/badge'
import { StatCard } from '@/features/dashboard/components/ui/stat-card'
import type {
  MediaConcurrency,
  MediaPool,
} from '@/features/media-operations/concurrency-schema'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

export function OperationsConcurrencyPanel(props: {
  report: MediaConcurrency
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const number = (value: number) => formatNumber(value, locale)
  const pools = props.report.pools
  const ready =
    pools.length > 0 && pools.every((pool) => pool.enabled && pool.reconciled)
  const numeric = 'text-right tabular-nums'
  const columns: StaticDataTableColumn<MediaPool>[] = [
    {
      id: 'pool',
      header: t('Shared upstream account'),
      className: 'min-w-44',
      cell: (pool) => (
        <div className='max-w-72 space-y-1 break-words'>
          <p className='font-medium'>{pool.label}</p>
          <Badge variant='secondary'>
            {pool.media_kind === 'video' ? t('Video') : t('Image')}
          </Badge>
        </div>
      ),
    },
    {
      id: 'channels',
      header: t('Channels'),
      cell: (pool) => (
        <span className='block max-w-48 break-words'>
          {pool.channel_ids.map(number).join(', ')}
        </span>
      ),
    },
    {
      id: 'configured',
      header: t('Configured policy limit'),
      className: numeric,
      cell: (pool) => number(pool.configured_limit),
    },
    {
      id: 'upstream',
      header: t('Known upstream limit'),
      className: numeric,
      cell: (pool) =>
        pool.known_upstream_limit === null
          ? t('Unknown')
          : number(pool.known_upstream_limit),
    },
    {
      id: 'effective',
      header: t('Effective admission limit'),
      className: numeric,
      cell: (pool) => number(pool.effective_limit),
    },
    {
      id: 'occupied',
      header: t('Occupied admission slots'),
      className: numeric,
      cell: (pool) => number(pool.occupied),
    },
    {
      id: 'unknown',
      header: t('UNKNOWN slots held'),
      className: numeric,
      cell: (pool) => number(pool.unknown),
    },
    {
      id: 'available',
      header: t('Available admission slots'),
      className: numeric,
      cell: (pool) =>
        pool.enabled && pool.reconciled
          ? number(pool.available_slots)
          : t('Unavailable'),
    },
    {
      id: 'status',
      header: t('Status'),
      cell: (pool) => {
        if (!pool.enabled) {
          return <Badge variant='secondary'>{t('Disabled')}</Badge>
        }
        return (
          <Badge variant='outline'>
            {pool.reconciled ? t('Ready') : t('Reconciliation required')}
          </Badge>
        )
      },
    },
  ]
  return (
    <div className='min-w-0 space-y-5'>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Admission slots are gateway observations, not exact upstream running concurrency. Unknown slots remain occupied. Current pools do not follow the selected history window.'
        )}
      </p>
      <section
        aria-label={t('Admission snapshot')}
        className='grid grid-cols-2 gap-4 rounded-xl border p-4 xl:grid-cols-4'
      >
        <StatCard
          title={t('Registered account pools')}
          value={number(pools.length)}
          description={t(
            'Image and video accounts registered with the gateway'
          )}
          icon={Radio}
          compactMobile
        />
        <StatCard
          title={t('Occupied admission slots')}
          value={number(pools.reduce((sum, pool) => sum + pool.occupied, 0))}
          description={t('Includes unresolved UNKNOWN slots')}
          icon={Layers}
          compactMobile
        />
        <StatCard
          title={t('UNKNOWN slots held')}
          value={number(pools.reduce((sum, pool) => sum + pool.unknown, 0))}
          description={t(
            'An unknown state does not prove that submission failed.'
          )}
          icon={CircleHelp}
          compactMobile
        />
        <div role='group' aria-label={t('Available admission slots')}>
          <StatCard
            title={t('Available admission slots')}
            value={
              ready
                ? number(
                    pools.reduce((sum, pool) => sum + pool.available_slots, 0)
                  )
                : t('Unknown')
            }
            description={t(
              'Available only when every displayed pool is enabled and reconciled'
            )}
            icon={Gauge}
            compactMobile
          />
        </div>
      </section>
      <p className='text-muted-foreground text-xs'>
        {t('Default admission policies')}: {t('Video')}{' '}
        {number(props.report.defaults.video)} · {t('Image')}{' '}
        {number(props.report.defaults.image)}
      </p>
      {pools.length ? (
        <StaticDataTable
          columns={columns}
          data={pools}
          getRowKey={(pool) => pool.pool_id}
          tableProps={{
            'aria-label': t('Image and video account admission pools'),
          }}
        />
      ) : (
        <EmptyState
          title={t('No registered image or video account pools')}
          description={t(
            'Pools appear when configured image or video keys are registered or first used.'
          )}
        />
      )}
    </div>
  )
}
