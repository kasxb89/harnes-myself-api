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
import { FileWarning, MessageSquareWarning } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { StatCard } from '@/features/dashboard/components/ui/stat-card'

import { useMediaFormat } from '../lib/format'
import type { MediaOperationsResult } from '../schema'

export function MediaHistorySummary(props: {
  data: MediaOperationsResult['history_summary']
}) {
  const { t } = useTranslation()
  const format = useMediaFormat()
  return (
    <section
      aria-label={t('Historical observations summary')}
      className='grid grid-cols-2 gap-4 rounded-xl border p-4'
    >
      <StatCard
        icon={FileWarning}
        title={t('Distinct error requests')}
        value={format.count(props.data.error_requests)}
        description={t('Gateway records deduplicated by request ID')}
        details={[
          {
            label: t('Submission error requests'),
            value: format.count(props.data.submit_error_requests),
          },
          {
            label: t('Terminal task error requests'),
            value: format.count(props.data.terminal_error_requests),
          },
          {
            label: t('Error requests with unknown stage'),
            value: format.count(props.data.unknown_stage_error_requests),
          },
        ]}
        compactMobile
      />
      <StatCard
        icon={MessageSquareWarning}
        title={t('Client report events')}
        value={format.count(props.data.client_report_events)}
        description={t('Client observations kept separate from gateway errors')}
        details={[
          {
            label: t('Client requests with correlation IDs'),
            value: format.count(props.data.correlated_client_requests),
          },
        ]}
        compactMobile
      />
    </section>
  )
}
