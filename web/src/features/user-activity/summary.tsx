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
import { Activity, AppWindow, Eye, Monitor, Play } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { StatCard } from '@/features/dashboard/components/ui/stat-card'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import type { ClientActivitySummary } from './types'
import { WorkloadSummary } from './workload-summary'

export function ActivitySummary(props: {
  summary?: ClientActivitySummary
  history?: boolean
  loading: boolean
  error: boolean
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const data = props.summary
  const presence = [
    {
      title: t('Currently online users'),
      value: data?.online_users,
      description: t('Users with a recent client heartbeat'),
      icon: Monitor,
    },
    {
      title: t('Active foreground users'),
      value: data?.foreground_users,
      description: t('Window focused with recent user activity'),
      icon: Eye,
    },
    {
      title: t('Background or idle users'),
      value: data
        ? Math.max(0, data.online_users - data.foreground_users)
        : undefined,
      description: t('Online without recent foreground activity'),
      icon: AppWindow,
    },
    {
      title: t('Online client processes'),
      value: data?.runtime_sessions,
      description: t('Client processes are not conversation sessions'),
      icon: Monitor,
    },
  ]
  const period = [
    {
      title: t('Client sessions started in range'),
      value: data?.started_sessions,
      description: t('Sessions started in the selected period'),
      icon: Play,
    },
    {
      title: t('Actual model calls in range'),
      value: data?.model_calls,
      description: t('Model usage is separate from software presence'),
      icon: Activity,
    },
  ]
  const cards = (items: typeof presence) =>
    items.map((card) => (
      <StatCard
        key={card.title}
        title={card.title}
        value={
          card.value === undefined
            ? t('Not collected')
            : formatNumber(card.value, locale)
        }
        description={card.description}
        icon={card.icon}
        loading={props.loading}
        error={props.error}
        compactMobile
      />
    ))
  return (
    <div className='space-y-4' aria-label={t('Client activity summary')}>
      <section aria-label={t('Global current presence')} className='space-y-2'>
        <h2 className='text-sm font-medium'>{t('Global current presence')}</h2>
        <p className='text-muted-foreground text-xs'>
          {t(
            'Current online counts and workload are global and do not follow the user or date filters.'
          )}
        </p>
        <div className='grid grid-cols-2 gap-3 rounded-xl border p-3 sm:gap-5 sm:p-4 xl:grid-cols-4'>
          {cards(presence)}
        </div>
      </section>
      <WorkloadSummary
        workload={data?.workload}
        loading={props.loading}
        error={props.error}
      />
      {(props.history ?? true) && (
        <section
          aria-label={t('Selected-period activity')}
          className='space-y-2'
        >
          <h2 className='text-sm font-medium'>
            {t('Selected-period activity')}
          </h2>
          <p className='text-muted-foreground text-xs'>
            {t(
              'Period metrics retain session-start counting. Durations are cumulative for those sessions, regardless of the history list time basis.'
            )}
          </p>
          <div className='grid grid-cols-2 gap-3 rounded-xl border p-3 sm:gap-5 sm:p-4'>
            {cards(period)}
          </div>
        </section>
      )}
    </div>
  )
}
