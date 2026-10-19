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

import { Button } from '@/components/ui/button'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { ActivityDuration, ActivityStatus, ActivityTimestamp } from './display'
import type { ClientActivitySession } from './types'

export function useActivityColumns(
  onDetails: (id: number) => void
): ColumnDef<ClientActivitySession>[] {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  return useMemo(
    () => [
      {
        id: 'user',
        header: t('User'),
        cell: ({ row }) => (
          <div className='max-w-44 min-w-0'>
            <div className='truncate font-medium' title={row.original.username}>
              {row.original.username}
            </div>
            <div className='text-muted-foreground text-xs'>
              ID {formatNumber(row.original.user_id, locale)}
            </div>
          </div>
        ),
        size: 180,
      },
      {
        accessorKey: 'status',
        header: t('Client status'),
        cell: ({ row }) => <ActivityStatus status={row.original.status} />,
        size: 160,
      },
      {
        id: 'times',
        header: t('Session start and last heartbeat'),
        cell: ({ row }) => (
          <div className='space-y-1'>
            <div>
              <span className='text-muted-foreground mr-2 text-xs'>
                {t('Session started')}
              </span>
              <ActivityTimestamp value={row.original.started_at} />
            </div>
            <div>
              <span className='text-muted-foreground mr-2 text-xs'>
                {t('Last heartbeat')}
              </span>
              <ActivityTimestamp value={row.original.last_seen_at} />
            </div>
          </div>
        ),
        size: 320,
      },
      {
        id: 'duration',
        header: t('Session cumulative time'),
        cell: ({ row }) => (
          <div className='space-y-1 text-xs'>
            <div>
              {t('Online')}:{' '}
              <ActivityDuration seconds={row.original.online_seconds} />
            </div>
            <div>
              {t('Foreground')}:{' '}
              <ActivityDuration seconds={row.original.foreground_seconds} />
            </div>
          </div>
        ),
        size: 200,
      },
      {
        id: 'client',
        header: t('Platform and version'),
        cell: ({ row }) => (
          <div className='max-w-48 text-xs break-all'>
            <div>{row.original.platform}</div>
            <div className='text-muted-foreground'>
              {row.original.app_version || '—'}
            </div>
          </div>
        ),
        size: 180,
      },
      {
        id: 'usage',
        header: t('Account model usage during session'),
        cell: ({ row }) => (
          <div className='space-y-1 text-xs'>
            <div>
              {t('Calls')}: {formatNumber(row.original.model_calls, locale)}
            </div>
            <div className='text-muted-foreground'>
              {t('Error records')}:{' '}
              {formatNumber(row.original.model_errors, locale)}
            </div>
          </div>
        ),
        size: 210,
      },
      {
        id: 'actions',
        header: t('Details'),
        cell: ({ row }) => (
          <Button
            size='sm'
            variant='ghost'
            onClick={() => onDetails(row.original.id)}
            aria-label={t('View activity for user', {
              username: row.original.username,
            })}
          >
            {t('Details')}
          </Button>
        ),
        size: 90,
      },
    ],
    [locale, onDetails, t]
  )
}
