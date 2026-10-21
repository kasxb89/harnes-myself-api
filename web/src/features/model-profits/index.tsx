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
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { getModelProfits } from './api'
import { CostRulesDialog } from './components/cost-rules-dialog'
import { ProfitFilters } from './components/profit-filters'
import { ProfitSummary } from './components/profit-summary'
import { ProfitTable } from './components/profit-table'
import { formatProfitCny } from './lib/format'
import { parseProfitPeriod, profitPreset } from './lib/period'

export function ModelProfitsPage() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const user = useAuthStore((state) => state.auth.user)
  const allowed = !!user && user.role >= ROLE.ADMIN
  const [draft, setDraft] = useState(() => profitPreset('month'))
  const [filters, setFilters] = useState(() => {
    const initial = parseProfitPeriod(draft)
    if (!initial) throw new Error('Invalid initial model profit period')
    return initial
  })
  const parsed = parseProfitPeriod(draft)
  const profits = useQuery({
    queryKey: ['model-profits', user?.id, filters],
    queryFn: () => getModelProfits(filters),
    enabled: allowed,
    retry: false,
  })
  const refresh = () => {
    void profits.refetch()
  }
  if (!allowed) return <ErrorState title={t('Access denied')} />
  return (
    <SectionPageLayout stackActionsOnMobile>
      <SectionPageLayout.Title>{t('Model profits')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <CostRulesDialog />
        <Button
          variant='outline'
          onClick={refresh}
          disabled={profits.isFetching}
        >
          {t('Refresh')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='min-w-0 space-y-4'>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Billed gross profit is customer balance charges minus known upstream costs. Gift balance consumption is included; this is not cash receipts or net profit.'
            )}
          </p>
          <ProfitFilters
            value={draft}
            onChange={setDraft}
            onApply={() => {
              if (parsed) setFilters(parsed)
            }}
            loading={profits.isFetching}
            invalid={!parsed}
          />
          {!parsed && (
            <Alert variant='destructive'>
              <AlertDescription>
                {t(
                  'Enter a valid start and end date; the end must not precede the start.'
                )}
              </AlertDescription>
            </Alert>
          )}
          {profits.isFetching && (
            <div role='status'>
              <LoadingState inline message={t('Loading model profits...')} />
            </div>
          )}
          {profits.isError && (
            <ErrorState
              title={t('Failed to load model profits')}
              onRetry={refresh}
            />
          )}
          {!profits.isError && profits.data && (
            <>
              {profits.data.consumption_logging_enabled === false && (
                <Alert variant='destructive'>
                  <AlertDescription>
                    {t(
                      'Consumption logging is disabled. This report may omit requests and cannot show complete profit.'
                    )}
                  </AlertDescription>
                </Alert>
              )}
              <ProfitSummary data={profits.data.summary} />
              {profits.data.summary.unknown_cost_events > 0 && (
                <Alert>
                  <AlertDescription>
                    {t(
                      '{{count}} billing events have unknown upstream costs. Gross profit and margin cover only events with configured price rules; missing costs are never counted as zero.',
                      { count: profits.data.summary.unknown_cost_events }
                    )}
                  </AlertDescription>
                </Alert>
              )}
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Pending settlement: {{count}} requests, {{amount}} reserved balance. Pending amounts are excluded from settled profit.',
                  {
                    count: formatNumber(
                      profits.data.summary.pending_requests,
                      locale
                    ),
                    amount: formatProfitCny(
                      profits.data.summary.pending_revenue_cny,
                      locale
                    ),
                  }
                )}
              </p>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Showing {{start}} to {{end}} inclusive, Asia/Shanghai. All amounts are CNY.',
                  {
                    start: new Date(
                      filters.start_timestamp * 1000 + 8 * 3600000
                    )
                      .toISOString()
                      .slice(0, 10),
                    end: new Date(
                      (filters.end_timestamp - 1) * 1000 + 8 * 3600000
                    )
                      .toISOString()
                      .slice(0, 10),
                  }
                )}
              </p>
              <ProfitTable rows={profits.data.rows} />
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Historical charges are converted using the current instance quota and exchange rate. Upstream prepayments, customer top-ups, operating costs and taxes are not deducted here.'
                )}
              </p>
              <p className='text-muted-foreground text-xs'>
                {t(
                  'Upstream costs are estimated from configured supplier prices, not reconciled supplier deductions or invoices.'
                )}
              </p>
            </>
          )}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
