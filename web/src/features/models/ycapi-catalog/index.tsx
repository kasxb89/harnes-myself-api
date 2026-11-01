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

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { useYcapiCatalog } from './api'
import { CatalogTable } from './catalog-table'
import { ConfiguredModelPricebook } from './configured-pricebook'
import { useConfiguredPricebook, pricebookStatesByModel } from './pricebook-api'
import { PricebookTable } from './pricebook-table'

function ApprovedYcapiCatalog() {
  const { t } = useTranslation()
  const query = useYcapiCatalog()
  const configured = useConfiguredPricebook()
  const states = configured.data
    ? pricebookStatesByModel(configured.data.rows)
    : undefined
  if (!query.allowed) return <ErrorState title={t('Access denied')} />
  if (query.isPending) {
    return <LoadingState message={t('Loading YCAPI models')} />
  }
  if (query.isError) {
    return (
      <ErrorState
        title={t('Failed to load YCAPI models')}
        onRetry={() => void query.refetch()}
      />
    )
  }
  if (!query.data) return <EmptyState title={t('No models found')} />
  return (
    <div className='space-y-4'>
      <div className='flex flex-wrap gap-2'>
        <Badge variant='secondary'>
          {t('43 models · 22 text · 8 image · 10 video · 3 audio')}
        </Badge>
        <Badge variant='outline'>{t('113 price rows')}</Badge>
        <Badge variant='outline'>{t('Approved price snapshot')}</Badge>
      </div>
      <Alert>
        <AlertTitle>{t('Approved model price list')}</AlertTitle>
        <AlertDescription>
          {t(
            'Prices are CNY. Video prices are per second for the listed clip durations. Procurement costs and pinned channels are visible only to the super administrator.'
          )}
        </AlertDescription>
      </Alert>
      <p className='text-muted-foreground text-xs'>
        {t('Price snapshot')}: {query.data.checkedAt} · {query.data.version}
      </p>
      <PricebookTable models={query.data.models} states={states} />
      <p className='text-muted-foreground text-xs'>
        {t('Official comparison exchange rate')}: 1 USD ={' '}
        {query.data.officialUsdCny} CNY
      </p>
      <p className='text-muted-foreground text-xs'>
        {t('Models not included')}:{' '}
        {query.data.exclusions.map((model) => model.upstreamModelId).join(', ')}
      </p>
      <CatalogTable models={query.data.models} states={states} />
    </div>
  )
}

export function YcapiCatalog() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  if (!user || user.role < ROLE.SUPER_ADMIN) {
    return <ErrorState title={t('Access denied')} />
  }
  return (
    <div className='h-full min-h-0 space-y-6 overflow-y-auto pr-1 pb-4'>
      <ConfiguredModelPricebook />
      <section
        aria-label={t('New YCAPI candidate prices')}
        className='space-y-3'
      >
        <h2 className='text-lg font-semibold'>
          {t('New YCAPI candidate prices')}
        </h2>
        <ApprovedYcapiCatalog />
      </section>
    </div>
  )
}
