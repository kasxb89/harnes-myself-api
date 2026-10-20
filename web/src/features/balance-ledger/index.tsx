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

import { DataTablePage, useDataTable } from '@/components/data-table'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { getBalanceLedger } from './api'
import { useBalanceColumns } from './columns'
import { LedgerFilters } from './filters'
import { LedgerSummary } from './summary'
import type { BalanceEntry, BalanceFilters } from './types'

const EMPTY_ENTRIES: BalanceEntry[] = []
const INITIAL_FILTERS: BalanceFilters = { p: 1, page_size: 20, category: 'all' }

export function BalanceLedger() {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const allowed = !!user && user.role >= ROLE.ADMIN
  const [filters, setFilters] = useState(INITIAL_FILTERS)
  const [draftUserId, setDraftUserId] = useState('')
  const [invalidUser, setInvalidUser] = useState(false)
  const invalidRange =
    filters.start_timestamp !== undefined &&
    filters.end_timestamp !== undefined &&
    filters.start_timestamp > filters.end_timestamp
  const invalid = invalidUser || invalidRange
  const ledger = useQuery({
    queryKey: ['balance-ledger', user?.id, filters],
    queryFn: () => getBalanceLedger(filters),
    enabled: allowed && !invalid,
    retry: false,
  })
  const columns = useBalanceColumns()
  const { table } = useDataTable({
    columns,
    data:
      allowed && !ledger.isError && !invalid
        ? (ledger.data?.items ?? EMPTY_ENTRIES)
        : EMPTY_ENTRIES,
    getRowId: (row) => row.id,
    totalCount:
      allowed && !ledger.isError && !invalid ? (ledger.data?.total ?? 0) : 0,
    pagination: { pageIndex: filters.p - 1, pageSize: filters.page_size },
    onPaginationChange: (updater) =>
      setFilters((previous) => {
        const current = {
          pageIndex: previous.p - 1,
          pageSize: previous.page_size,
        }
        const next = typeof updater === 'function' ? updater(current) : updater
        return {
          ...previous,
          p: next.pageSize === previous.page_size ? next.pageIndex + 1 : 1,
          page_size: next.pageSize,
        }
      }),
    enableRowSelection: false,
    enableSorting: false,
    manualFiltering: true,
    manualPagination: true,
  })
  const update = (patch: Partial<BalanceFilters>) =>
    setFilters((previous) => ({ ...previous, ...patch, p: 1 }))
  const search = () => {
    const value = draftUserId.trim()
    const userId = value ? Number(value) : undefined
    if (
      value &&
      (!/^\d+$/.test(value) ||
        !Number.isSafeInteger(userId) ||
        userId === undefined ||
        userId <= 0)
    ) {
      setInvalidUser(true)
      return
    }
    setInvalidUser(false)
    update({ user_id: userId })
  }
  const refresh = () => {
    if (!invalid) void ledger.refetch()
  }
  if (!allowed) return <ErrorState title={t('Access denied')} />
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>{t('Balance ledger')}</SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button
          variant='outline'
          onClick={refresh}
          disabled={invalid || ledger.isFetching}
        >
          {t('Refresh')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='min-w-0 space-y-4'>
          <p className='text-muted-foreground text-sm'>
            {t(
              'Review registration gifts, actual payments and administrator changes separately. This ledger records balance funding and adjustments; model consumption is in Usage Logs.'
            )}
          </p>
          <LedgerSummary
            summary={
              !invalid && !ledger.isError ? ledger.data?.summary : undefined
            }
            loading={ledger.isPending && !invalid}
            error={ledger.isError || invalid}
          />
          <DataTablePage
            table={table}
            columns={columns}
            isLoading={ledger.isPending && !invalid}
            isFetching={ledger.isFetching}
            emptyTitle={
              ledger.isError
                ? t('Failed to load balance ledger')
                : t('No balance records')
            }
            emptyDescription={t('Try another balance source, user or period.')}
            paginationInFooter={false}
            fixedHeight={false}
            applyHeaderSize
            toolbar={
              <div className='space-y-2'>
                <LedgerFilters
                  table={table}
                  filters={filters}
                  draftUserId={draftUserId}
                  onDraftUserIdChange={setDraftUserId}
                  onUpdate={update}
                  onSearch={search}
                  onReset={() => {
                    setDraftUserId('')
                    setInvalidUser(false)
                    setFilters(INITIAL_FILTERS)
                  }}
                  loading={ledger.isFetching}
                  invalidUser={invalidUser}
                />
                {invalidRange && (
                  <Alert variant='destructive'>
                    <AlertDescription>
                      {t('End time must be after start time')}
                    </AlertDescription>
                  </Alert>
                )}
                {invalidUser && (
                  <Alert variant='destructive'>
                    <AlertDescription>
                      {t('Enter a positive whole-number user ID')}
                    </AlertDescription>
                  </Alert>
                )}
                {ledger.isError && (
                  <Alert variant='destructive'>
                    <AlertDescription className='flex items-center justify-between gap-2'>
                      <span>{t('Failed to load balance ledger')}</span>
                      <Button size='sm' variant='outline' onClick={refresh}>
                        {t('Retry')}
                      </Button>
                    </AlertDescription>
                  </Alert>
                )}
              </div>
            }
          />
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
