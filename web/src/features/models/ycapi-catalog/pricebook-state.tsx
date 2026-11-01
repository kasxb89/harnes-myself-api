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

import { Badge } from '@/components/ui/badge'

export type ModelPricebookState =
  | 'enabled'
  | 'disabled'
  | 'not_configured'
  | 'excluded'
export function PricebookStateBadge(props: { state?: ModelPricebookState }) {
  const { t } = useTranslation()
  let label = t('Unknown')
  if (props.state === 'enabled') label = t('Enabled')
  else if (props.state === 'disabled') label = t('Disabled')
  else if (props.state === 'not_configured') {
    label = t('Not connected to a channel')
  } else if (props.state === 'excluded') {
    label = t('Not in active candidate catalog')
  }
  return (
    <Badge variant={props.state === 'enabled' ? 'secondary' : 'outline'}>
      {label}
    </Badge>
  )
}
