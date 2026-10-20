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
import type { Table } from '@tanstack/react-table'
import { useTranslation } from 'react-i18next'

import { Combobox } from '@/components/ui/combobox'
import { CompactDateTimeRangePicker } from '@/features/usage-logs/components/compact-date-time-range-picker'
import {
  LogsFilterField,
  LogsFilterInput,
  LogsFilterToolbar,
} from '@/features/usage-logs/components/logs-filter-toolbar'

import { BALANCE_CATEGORIES } from './constants'
import type { BalanceCategory, BalanceEntry, BalanceFilters } from './types'

export function LedgerFilters(props: {
  table: Table<BalanceEntry>
  filters: BalanceFilters
  draftUserId: string
  onDraftUserIdChange: (value: string) => void
  onUpdate: (patch: Partial<BalanceFilters>) => void
  onSearch: () => void
  onReset: () => void
  loading: boolean
  invalidUser: boolean
}) {
  const { t } = useTranslation()
  return (
    <LogsFilterToolbar
      table={props.table}
      primaryFilters={
        <>
          <LogsFilterField>
            <Combobox
              aria-label={t('Balance source')}
              value={props.filters.category}
              options={Object.entries(BALANCE_CATEGORIES).map(
                ([value, label]) => ({ value, label: t(label) })
              )}
              onValueChange={(value: string | null) => {
                if (value) {
                  props.onUpdate({ category: value as BalanceCategory })
                }
              }}
              className='w-full'
            />
          </LogsFilterField>
          <LogsFilterField>
            <LogsFilterInput
              aria-label={t('User ID')}
              placeholder={t('User ID')}
              inputMode='numeric'
              aria-invalid={props.invalidUser}
              value={props.draftUserId}
              onChange={(event) =>
                props.onDraftUserIdChange(event.target.value)
              }
              onKeyDown={(event) => {
                if (event.key === 'Enter') props.onSearch()
              }}
            />
          </LogsFilterField>
          <LogsFilterField wide>
            <CompactDateTimeRangePicker
              start={
                props.filters.start_timestamp === undefined
                  ? undefined
                  : new Date(props.filters.start_timestamp * 1000)
              }
              end={
                props.filters.end_timestamp === undefined
                  ? undefined
                  : new Date(props.filters.end_timestamp * 1000)
              }
              onChange={({ start, end }) =>
                props.onUpdate({
                  start_timestamp: start
                    ? Math.floor(start.getTime() / 1000)
                    : undefined,
                  end_timestamp: end
                    ? Math.floor(end.getTime() / 1000)
                    : undefined,
                })
              }
            />
          </LogsFilterField>
        </>
      }
      hasActiveFilters={
        props.filters.category !== 'all' ||
        !!props.draftUserId ||
        props.filters.start_timestamp !== undefined ||
        props.filters.end_timestamp !== undefined
      }
      onSearch={props.onSearch}
      onReset={props.onReset}
      searchLoading={props.loading}
    />
  )
}
