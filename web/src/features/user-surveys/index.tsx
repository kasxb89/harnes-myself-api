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
import type { ColumnDef } from '@tanstack/react-table'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { TimestampCell } from '@/components/activity-time-cell'
import {
  BadgeListCell,
  DataTablePage,
  TruncatedCell,
  useDataTable,
} from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { StatusBadge } from '@/components/status-badge'
import { TableId } from '@/components/table-id'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import {
  DetailRow,
  DetailSection,
} from '@/features/usage-logs/components/dialogs/log-detail-layout'
import {
  LogsFilterField,
  LogsFilterInput,
  LogsFilterToolbar,
} from '@/features/usage-logs/components/logs-filter-toolbar'
import { canAccessAdminRoute } from '@/lib/admin-permissions'
import { formatTimestampToDate } from '@/lib/format'
import { useAuthStore } from '@/stores/auth-store'

import {
  surveyOptionLabel,
  getSurveyRegistrations,
  type SurveyFilters,
  type SurveyRegistration,
  type SurveyStatus,
} from './api'
import { SurveyConfigEditor } from './config-editor'

const EMPTY: SurveyRegistration[] = []
const INITIAL: SurveyFilters = { p: 1, page_size: 20 }

export function UserSurveys() {
  const { t, i18n } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const allowed = canAccessAdminRoute(user, '/user-surveys')
  const [filters, setFilters] = useState<SurveyFilters>(INITIAL)
  const [configOpen, setConfigOpen] = useState(false)
  const [phone, setPhone] = useState('')
  const [selected, setSelected] = useState<SurveyRegistration | null>(null)
  const query = useQuery({
    queryKey: ['user-surveys', user?.id, filters],
    queryFn: ({ signal }) => getSurveyRegistrations(filters, signal),
    enabled: allowed,
    retry: false,
  })
  const data = allowed && !query.isError ? query.data : undefined
  const statusLabels = useMemo<Record<SurveyStatus, string>>(
    () => ({
      pending: t('Awaiting questionnaire'),
      deferred: t('Deferred'),
      submitted: t('Submitted'),
    }),
    [t]
  )
  const optionLabels = useMemo(
    () =>
      new Map(
        (data?.options ?? []).map((option) => [
          option.id,
          surveyOptionLabel(option, i18n.resolvedLanguage || i18n.language),
        ])
      ),
    [data?.options, i18n.resolvedLanguage, i18n.language]
  )
  const columns = useMemo<ColumnDef<SurveyRegistration>[]>(
    () => [
      {
        accessorKey: 'user_id',
        header: t('User ID'),
        size: 90,
        cell: ({ row }) => <TableId value={row.original.user_id} />,
      },
      {
        accessorKey: 'phone_e164',
        header: t('Phone number'),
        size: 180,
        cell: ({ row }) => (
          <TruncatedCell tabIndex={0}>{row.original.phone_e164}</TruncatedCell>
        ),
      },
      {
        accessorKey: 'status',
        header: t('Questionnaire status'),
        size: 160,
        cell: ({ row }) => (
          <StatusBadge
            variant={
              row.original.status === 'submitted' ? 'success' : 'neutral'
            }
            copyable={false}
            label={statusLabels[row.original.status]}
          />
        ),
      },
      {
        accessorKey: 'selected_option_ids',
        header: t('Product use scenarios'),
        size: 260,
        cell: ({ row }) => (
          <BadgeListCell
            max={2}
            expandable
            expandLabel={t('Show all scenarios')}
            items={row.original.selected_option_ids.map((id) => {
              const option = row.original.selected_options?.find(
                (item) => item.id === id
              )
              return (
                <StatusBadge
                  key={id}
                  variant='info'
                  copyable={false}
                  label={
                    option
                      ? surveyOptionLabel(
                          option,
                          i18n.resolvedLanguage || i18n.language
                        )
                      : (optionLabels.get(id) ?? id)
                  }
                />
              )
            })}
          />
        ),
      },
      {
        accessorKey: 'notes',
        header: t('Notes'),
        size: 240,
        cell: ({ row }) => (
          <TruncatedCell
            tabIndex={0}
            tooltipClassName='max-h-64 overflow-y-auto whitespace-pre-wrap'
          >
            {row.original.notes || '—'}
          </TruncatedCell>
        ),
      },
      {
        accessorKey: 'registered_at',
        header: t('Registered at'),
        size: 170,
        cell: ({ row }) => (
          <TimestampCell
            timestamp={row.original.registered_at}
            format='absolute'
            justNowLabel={t('Just now')}
          />
        ),
      },
      {
        accessorKey: 'submitted_at',
        header: t('Submitted at'),
        size: 170,
        cell: ({ row }) => (
          <TimestampCell
            timestamp={row.original.submitted_at}
            format='absolute'
            justNowLabel={t('Just now')}
          />
        ),
      },
      {
        id: 'actions',
        header: t('Actions'),
        size: 100,
        cell: ({ row }) => (
          <Button
            size='sm'
            variant='ghost'
            onClick={() => setSelected(row.original)}
          >
            {t('Details')}
          </Button>
        ),
      },
    ],
    [t, optionLabels, statusLabels, i18n.resolvedLanguage, i18n.language]
  )
  const { table } = useDataTable({
    columns,
    data: data?.items ?? EMPTY,
    totalCount: data?.total ?? 0,
    getRowId: (row) => String(row.user_id),
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
  const update = (patch: Partial<SurveyFilters>) => {
    setSelected(null)
    setFilters((previous) => ({ ...previous, ...patch, p: 1 }))
  }
  const refresh = () => {
    setSelected(null)
    void query.refetch()
  }
  if (!allowed) return <ErrorState title={t('Access denied')} />
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('User questionnaires')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button variant='outline' onClick={() => setConfigOpen(true)}>
          {t('Questionnaire options')}
        </Button>
        <Button variant='outline' onClick={refresh} disabled={query.isFetching}>
          {t('Refresh')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <p className='text-muted-foreground mb-4 text-sm'>
          {t(
            'New phone registrations appear here. Users can select several scenarios, leave optional notes or fill in the questionnaire later. Existing accounts are not added automatically.'
          )}
        </p>
        <DataTablePage
          table={table}
          columns={columns}
          isLoading={query.isPending}
          isFetching={query.isFetching}
          emptyTitle={
            query.isError
              ? t('Failed to load questionnaires')
              : t('No questionnaire registrations')
          }
          emptyDescription={t(
            'Only new phone accounts registered after this feature is enabled appear here.'
          )}
          paginationInFooter={false}
          className='h-auto'
          applyHeaderSize
          toolbar={
            <div className='space-y-2'>
              <LogsFilterToolbar
                table={table}
                hasActiveFilters={
                  !!(
                    phone ||
                    filters.phone ||
                    filters.status ||
                    filters.option_id
                  )
                }
                searchLoading={query.isFetching}
                onSearch={() => update({ phone: phone.trim() || undefined })}
                onReset={() => {
                  setPhone('')
                  setSelected(null)
                  setFilters(INITIAL)
                }}
                primaryFilters={
                  <>
                    <LogsFilterField>
                      <LogsFilterInput
                        aria-label={t('Phone number')}
                        placeholder={t('Search phone number')}
                        value={phone}
                        maxLength={32}
                        onChange={(event) => setPhone(event.target.value)}
                        onKeyDown={(event) => {
                          if (event.key === 'Enter') {
                            update({ phone: phone.trim() || undefined })
                          }
                        }}
                      />
                    </LogsFilterField>
                    <LogsFilterField>
                      <Combobox
                        aria-label={t('Questionnaire status')}
                        value={filters.status ?? 'all'}
                        options={[
                          { value: 'all', label: t('All statuses') },
                          ...Object.entries(statusLabels).map(
                            ([value, label]) => ({ value, label })
                          ),
                        ]}
                        onValueChange={(value) => {
                          if (value) {
                            update({
                              status:
                                value === 'all'
                                  ? undefined
                                  : (value as SurveyStatus),
                            })
                          }
                        }}
                      />
                    </LogsFilterField>
                    <LogsFilterField>
                      <Combobox
                        aria-label={t('Product use scenarios')}
                        value={filters.option_id ?? 'all'}
                        options={[
                          { value: 'all', label: t('All scenarios') },
                          ...(data?.options ?? []).map((option) => ({
                            value: option.id,
                            label: surveyOptionLabel(
                              option,
                              i18n.resolvedLanguage || i18n.language
                            ),
                          })),
                        ]}
                        onValueChange={(value) => {
                          if (value) {
                            update({
                              option_id: value === 'all' ? undefined : value,
                            })
                          }
                        }}
                      />
                    </LogsFilterField>
                  </>
                }
              />
              {query.isError && (
                <Alert variant='destructive'>
                  <AlertDescription className='flex items-center justify-between gap-2'>
                    <span>{t('Failed to load questionnaires')}</span>
                    <Button size='sm' variant='outline' onClick={refresh}>
                      {t('Retry')}
                    </Button>
                  </AlertDescription>
                </Alert>
              )}
            </div>
          }
        />
        {configOpen && (
          <SurveyConfigEditor onClose={() => setConfigOpen(false)} />
        )}
        <Dialog
          open={selected !== null && !query.isError}
          onOpenChange={(open) => {
            if (!open) setSelected(null)
          }}
          title={t('Questionnaire details')}
          description={t(
            'Registration identity and submitted answers are recorded by the server.'
          )}
          bodyClassName='space-y-4'
        >
          {selected && (
            <>
              <DetailSection label={t('Registration')}>
                <DetailRow
                  label={t('User ID')}
                  value={<TableId value={selected.user_id} />}
                />
                <DetailRow
                  label={t('Phone number')}
                  value={selected.phone_e164}
                />
                <DetailRow
                  label={t('Questionnaire status')}
                  value={statusLabels[selected.status]}
                />
                <DetailRow
                  label={t('Registered at')}
                  value={formatTimestampToDate(selected.registered_at)}
                />
                <DetailRow
                  label={t('Submitted at')}
                  value={
                    selected.submitted_at
                      ? formatTimestampToDate(selected.submitted_at)
                      : '—'
                  }
                />
                <DetailRow
                  label={t('Deferred at')}
                  value={
                    selected.deferred_at
                      ? formatTimestampToDate(selected.deferred_at)
                      : '—'
                  }
                />
              </DetailSection>
              <DetailSection label={t('Product use scenarios')}>
                <p className='text-sm break-words'>
                  {selected.selected_option_ids
                    .map((id) => {
                      const option = selected.selected_options?.find(
                        (item) => item.id === id
                      )
                      return option
                        ? surveyOptionLabel(
                            option,
                            i18n.resolvedLanguage || i18n.language
                          )
                        : (optionLabels.get(id) ?? id)
                    })
                    .join(' · ') || '—'}
                </p>
              </DetailSection>
              <DetailSection label={t('Notes')}>
                <p className='text-sm break-words whitespace-pre-wrap'>
                  {selected.notes || t('No notes')}
                </p>
              </DetailSection>
            </>
          )}
        </Dialog>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
