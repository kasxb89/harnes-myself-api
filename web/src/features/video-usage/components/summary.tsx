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
import { Activity, Layers, Timer } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { StatCard } from '@/features/dashboard/components/ui/stat-card'

import { useVideoFormat } from '../lib/format'
import type { VideoUsageResult } from '../schema'

export function VideoUsageSummary(props: {
  summary: VideoUsageResult['summary']
}) {
  const { t } = useTranslation()
  const format = useVideoFormat()
  const summary = props.summary
  return (
    <>
      <section
        aria-label={t('Video usage summary')}
        className='grid grid-cols-2 gap-4 rounded-xl border p-4 xl:grid-cols-4'
      >
        <StatCard
          icon={Activity}
          title={t('Recorded in-flight tasks')}
          value={format.count(summary.current.inflight)}
          description={t(
            'Known nonterminal states; unknown states are separate'
          )}
          compactMobile
        />
        <StatCard
          icon={Layers}
          title={t('Recorded interval peak')}
          value={
            summary.peak.value === null
              ? t('Unknown')
              : format.count(summary.peak.value)
          }
          description={t('Valid gateway task intervals, not upstream capacity')}
          details={[
            {
              label: t('Valid / excluded intervals'),
              value: `${format.count(summary.peak.interval_samples)} / ${format.count(summary.peak.excluded_intervals)}`,
            },
          ]}
          compactMobile
        />
        <StatCard
          icon={Timer}
          title={t('Terminal turnaround P50')}
          value={format.duration(summary.history.turnaround.p50_seconds)}
          description={t('Gateway-recorded submit to terminal timestamps')}
          details={[
            {
              label: t('Valid / missing / invalid samples'),
              value: `${format.count(summary.history.turnaround.valid_samples)} / ${format.count(summary.history.turnaround.missing_samples)} / ${format.count(summary.history.turnaround.invalid_samples)}`,
            },
          ]}
          compactMobile
        />
        <StatCard
          icon={Timer}
          title={t('Terminal turnaround P95')}
          value={format.duration(summary.history.turnaround.p95_seconds)}
          description={t('Gateway-recorded submit to terminal timestamps')}
          compactMobile
        />
      </section>
      <div className='text-muted-foreground space-y-1 text-xs'>
        <p>
          {t('Peak intervals: {{valid}} valid / {{excluded}} excluded.', {
            valid: format.count(summary.peak.interval_samples),
            excluded: format.count(summary.peak.excluded_intervals),
          })}
        </p>
        <p>
          {t(
            'Turnaround samples: {{valid}} valid / {{missing}} missing / {{invalid}} invalid.',
            {
              valid: format.count(summary.history.turnaround.valid_samples),
              missing: format.count(summary.history.turnaround.missing_samples),
              invalid: format.count(summary.history.turnaround.invalid_samples),
            }
          )}
        </p>
      </div>
    </>
  )
}
