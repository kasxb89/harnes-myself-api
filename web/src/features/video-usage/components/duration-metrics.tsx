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

import { useVideoFormat } from '../lib/format'
import type { VideoDuration } from '../schema'

export function VideoDurationMetrics(props: { data: VideoDuration }) {
  const { t } = useTranslation()
  const format = useVideoFormat()
  return (
    <div className='min-w-48 space-y-1 text-xs'>
      <div>
        {t('Mean')}: {format.duration(props.data.mean_seconds)}
      </div>
      <div>
        P50: {format.duration(props.data.p50_seconds)} · P95:{' '}
        {format.duration(props.data.p95_seconds)}
      </div>
      <div className='text-muted-foreground whitespace-normal'>
        {t(
          'Samples: {{valid}} valid ({{zero}} zero) / {{missing}} missing / {{invalid}} invalid',
          {
            valid: format.count(props.data.valid_samples),
            missing: format.count(props.data.missing_samples),
            invalid: format.count(props.data.invalid_samples),
            zero: format.count(props.data.zero_samples),
          }
        )}
      </div>
    </div>
  )
}
