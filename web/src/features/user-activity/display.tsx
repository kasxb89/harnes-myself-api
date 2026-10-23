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

import { StatusBadge } from '@/components/status-badge'
import { toIntlLocale } from '@/i18n/languages'
import dayjs from '@/lib/dayjs'
import { formatNumber } from '@/lib/format'

import type { ClientActivitySession, WorkloadObservationStatus } from './types'

export function ActivityTimestamp(props: { value: number }) {
  if (!props.value) return <span>—</span>
  return (
    <time
      className='text-xs whitespace-nowrap tabular-nums'
      dateTime={new Date(props.value * 1000).toISOString()}
    >
      {dayjs.unix(props.value).format('YYYY-MM-DD HH:mm:ss Z')}
    </time>
  )
}
export function ActivityDuration(props: { seconds: number }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const seconds = Math.max(0, Math.floor(props.seconds))
  return (
    <span className='tabular-nums'>
      {t('Activity duration', {
        hours: formatNumber(Math.floor(seconds / 3600), locale),
        minutes: formatNumber(Math.floor((seconds % 3600) / 60), locale),
        seconds: formatNumber(seconds % 60, locale),
      })}
    </span>
  )
}
export function ActivityStatus(props: {
  status: ClientActivitySession['status']
}) {
  const { t } = useTranslation()
  if (props.status === 'foreground') {
    return (
      <StatusBadge
        variant='success'
        label={t('Active foreground')}
        copyable={false}
      />
    )
  }
  if (props.status === 'background') {
    return (
      <StatusBadge
        variant='info'
        label={t('Background or idle')}
        copyable={false}
      />
    )
  }
  return <StatusBadge variant='neutral' label={t('Offline')} copyable={false} />
}

export function ObservationStatus(props: {
  status?: WorkloadObservationStatus
}) {
  const { t } = useTranslation()
  const labels: Record<WorkloadObservationStatus, string> = {
    live: t('Current observation'),
    unknown: t('State unknown'),
    ended: t('Explicitly ended'),
    expired: t('Observation expired'),
    not_reported: t('Not in latest report'),
    not_collected: t('Not collected'),
  }
  return (
    <StatusBadge
      variant={props.status === 'live' ? 'success' : 'neutral'}
      label={
        props.status
          ? (labels[props.status] ?? t('Not collected'))
          : t('Not collected')
      }
      copyable={false}
    />
  )
}
