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

import type { ModelProfitResult } from '../types'

export function ProfitFundingNotice(props: { report: ModelProfitResult }) {
  const { t } = useTranslation()
  let message =
    'Funding allocation metadata is unavailable from this service; paid and gifted consumption cannot be confirmed.'
  if (props.report.funding_collection_status === 'not_collected') {
    message =
      'Paid-versus-gift funding allocations are not collected in this version. New consumption also remains unverified until a funding ledger is implemented.'
  } else if (
    props.report.funding_collection_status === 'collected' &&
    props.report.summary.funding_policy === 'fifo'
  ) {
    message =
      'Funding allocations are recorded for new operations. Unverifiable historical balances, non-CNY receipts and subscription funding remain pending confirmation.'
  }
  return (
    <Alert>
      <AlertDescription>{t(message)}</AlertDescription>
    </Alert>
  )
}
