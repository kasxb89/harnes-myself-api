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
import { Activity, CircleHelp, Clock, Layers } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { StatCard } from '@/features/dashboard/components/ui/stat-card'
import { MediaCurrentTable } from '@/features/media-operations/components/current-table'
import { MediaErrorBreakdown } from '@/features/media-operations/components/error-breakdown'
import { MediaHistorySummary } from '@/features/media-operations/components/history-summary'
import { MediaHistoryTable } from '@/features/media-operations/components/history-table'
import { MediaPollTable } from '@/features/media-operations/components/poll-table'
import type { MediaOperationsResult } from '@/features/media-operations/schema'
import { ActivityTimestamp } from '@/features/user-activity/display'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

export function OperationsMediaPanel(props: { report: MediaOperationsResult }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const summary = props.report.summary
  return (
    <div className='min-w-0 space-y-5'>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Persisted task states are observations, not a complete live queue. Current task states are global; historical errors and accepted tasks follow the query window.'
        )}
      </p>
      <p className='text-muted-foreground text-xs'>
        {t('Server observation time')}:{' '}
        <ActivityTimestamp value={props.report.as_of} />
      </p>
      <section
        aria-label={t('Current task snapshot')}
        className='grid grid-cols-2 gap-4 rounded-xl border p-4 xl:grid-cols-4'
      >
        <StatCard
          title={t('Submitted or running')}
          value={formatNumber(summary.submitted + summary.in_progress, locale)}
          description={t('Persisted submitted and in-progress task states')}
          icon={Activity}
          compactMobile
        />
        <StatCard
          title={t('Upstream-reported queued')}
          value={formatNumber(summary.queued, locale)}
          description={t(
            'Recorded upstream queue states, not a local scheduling queue'
          )}
          icon={Clock}
          compactMobile
        />
        <StatCard
          title={t('Recorded not started')}
          value={formatNumber(summary.not_start, locale)}
          description={t('Persisted gateway task records')}
          icon={Layers}
          compactMobile
        />
        <StatCard
          title={t('Recorded unknown states')}
          value={formatNumber(summary.unknown, locale)}
          description={t(
            'An unknown state does not prove that submission failed.'
          )}
          icon={CircleHelp}
          compactMobile
        />
      </section>
      <section className='min-w-0 space-y-3'>
        <h2 className='font-medium'>{t('Current task records')}</h2>
        <MediaCurrentTable rows={props.report.models} />
      </section>
      <MediaHistorySummary data={props.report.history_summary} />
      <section className='min-w-0 space-y-3'>
        <h2 className='font-medium'>{t('Task history by model')}</h2>
        <MediaHistoryTable rows={props.report.models} />
      </section>
      <MediaErrorBreakdown history={props.report.history_summary} />
      <section className='min-w-0 space-y-3'>
        <h2 className='font-medium'>{t('Polling observations')}</h2>
        <MediaPollTable rows={props.report.models} />
      </section>
    </div>
  )
}
