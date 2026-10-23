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

import { Alert, AlertDescription } from '@/components/ui/alert'
import { ProfitFormulas } from '@/features/model-profits/components/profit-formulas'
import { ProfitFundingDetails } from '@/features/model-profits/components/profit-funding-details'
import { ProfitFundingNotice } from '@/features/model-profits/components/profit-funding-notice'
import { ProfitSummary } from '@/features/model-profits/components/profit-summary'
import { ProfitTable } from '@/features/model-profits/components/profit-table'

import type { OperationsSnapshot } from '../api'
import { OperationsActivityPanel } from './activity-panel'
import { OperationsConcurrencyPanel } from './concurrency-panel'
import { OperationsLogsPanel } from './logs-panel'
import { OperationsMediaPanel } from './media-panel'
import { OperationsScienceCharts } from './science-charts'

function OperationsSnapshotDetails(props: { snapshot: OperationsSnapshot }) {
  const { t } = useTranslation()
  const snapshot = props.snapshot
  switch (snapshot.tab) {
    case 'profit':
      return (
        <div className='min-w-0 space-y-5'>
          <ProfitFundingNotice report={snapshot.report} />
          {snapshot.report.consumption_logging_enabled === false && (
            <Alert variant='destructive'>
              <AlertDescription>
                {t(
                  'Consumption logging is disabled. Profit observations may be incomplete.'
                )}
              </AlertDescription>
            </Alert>
          )}
          <ProfitSummary data={snapshot.report.summary} />
          <ProfitFundingDetails data={snapshot.report.summary} />
          <ProfitFormulas />
          <p className='text-muted-foreground text-xs'>
            {t(
              'Unit prices are net averages per request in this query window, not current advertised token, image or second prices. Missing cost evidence remains pending confirmation.'
            )}
          </p>
          <ProfitTable rows={snapshot.report.rows} />
        </div>
      )
    case 'concurrency':
      return <OperationsConcurrencyPanel report={snapshot.report} />
    case 'usage':
    case 'errors':
      return <OperationsLogsPanel snapshot={snapshot} />
    case 'image':
    case 'video':
      return <OperationsMediaPanel report={snapshot.report} />
    case 'activity':
      return <OperationsActivityPanel snapshot={snapshot} />
  }
}

export function OperationsSnapshotContent(props: {
  snapshot: OperationsSnapshot
  showCharts?: boolean
}) {
  return (
    <div className='min-w-0 space-y-5'>
      {props.showCharts !== false && (
        <OperationsScienceCharts snapshot={props.snapshot} />
      )}
      <OperationsSnapshotDetails snapshot={props.snapshot} />
    </div>
  )
}
