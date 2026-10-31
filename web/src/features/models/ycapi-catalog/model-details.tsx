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

import { CopyButton } from '@/components/copy-button'
import { Dialog } from '@/components/dialog'

import { YcapiModelPrices } from './model-prices'
import { QuotePreviewForm } from './quote-preview'
import type { YcapiModel } from './types'

export function YcapiModelDetails(props: {
  model: YcapiModel
  onClose: () => void
}) {
  const { t } = useTranslation()
  return (
    <Dialog
      open
      title={props.model.name}
      description={props.model.id}
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      contentClassName='sm:max-w-6xl'
    >
      <div className='space-y-5'>
        <div className='flex items-center gap-2 text-sm'>
          <span>{t('Upstream model ID')}:</span>
          <code>{props.model.upstreamModelId}</code>
          <CopyButton value={props.model.upstreamModelId} />
        </div>
        <QuotePreviewForm key={props.model.id} model={props.model} />
        <YcapiModelPrices model={props.model} />
      </div>
    </Dialog>
  )
}
