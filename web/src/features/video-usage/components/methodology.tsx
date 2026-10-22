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

export function VideoUsageMethodology() {
  const { t } = useTranslation()
  return (
    <details className='text-muted-foreground space-y-2 text-sm'>
      <summary className='cursor-pointer font-medium'>
        {t('How to read these records')}
      </summary>
      <p>
        {t(
          'Submit, start and finish are saved task timestamps. Historical records can include upstream-reported times. They are not exact rendering or client-click durations.'
        )}
      </p>
      <p>
        {t(
          'Interval peaks use valid gateway [submit, finish) records, including intervals crossing the selected window. Same-second finishes precede starts. Unknown states and invalid intervals are excluded; no available slots or utilization are inferred.'
        )}
      </p>
      <p>
        {t(
          'History uses a half-open [start, end) submission window. Current states are independent of that window. Duration statistics use terminal records with valid timestamps; missing start times remain unknown.'
        )}
      </p>
      <p>
        {t(
          'Desktop source is a client declaration, not verified identity. Older records without that declaration remain unknown. H3 video seconds are upstream usage reports, not measured output duration. This page does not provide client online or foreground time attributable to video models.'
        )}
      </p>
      <p>
        {t(
          'P50/P95 use the nearest-rank method; zero durations are included in valid samples.'
        )}
      </p>
    </details>
  )
}
