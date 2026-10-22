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
import { Activity, CircleHelp, Layers, Clock } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { StatCard } from '@/features/dashboard/components/ui/stat-card'

import { useMediaFormat } from '../lib/format'
import type { MediaOperationsResult } from '../schema'

export function MediaOperationsSnapshot(props: {
  data: MediaOperationsResult
}) {
  const { t } = useTranslation()
  const format = useMediaFormat()
  return (
    <section
      aria-label={t('Current task snapshot')}
      className='grid grid-cols-2 gap-4 rounded-xl border p-4 xl:grid-cols-4'
    >
      <StatCard
        icon={Activity}
        title={t('Submitted or running')}
        value={format.count(
          props.data.summary.submitted + props.data.summary.in_progress
        )}
        description={t('Persisted submitted and in-progress task states')}
        compactMobile
      />
      <StatCard
        icon={Clock}
        title={t('Upstream-reported queued')}
        value={format.count(props.data.summary.queued)}
        description={t(
          'Recorded upstream queue states, not a local scheduling queue'
        )}
        compactMobile
      />
      <StatCard
        icon={CircleHelp}
        title={t('Recorded unknown states')}
        value={format.count(props.data.summary.unknown)}
        description={t(
          'An unknown state does not prove that submission failed.'
        )}
        compactMobile
      />
      <StatCard
        icon={Layers}
        title={t('Configured concurrency')}
        value={t('Unknown')}
        description={t(
          'Real upstream account limits have not been configured.'
        )}
        compactMobile
      />
    </section>
  )
}
