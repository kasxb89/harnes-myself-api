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
import type { ColumnDef } from '@tanstack/react-table'
import { useMemo, useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  DataTablePage,
  StaticDataTable,
  useDataTable,
} from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { formatProfitCny } from '@/features/model-profits/lib/format'
import { shanghaiDateTime } from '@/features/model-profits/lib/period'
import { ModelPriceCell } from '@/features/pricing/components/model-price-cell'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import {
  useConfiguredPricebook,
  pricebookStatesByModel,
  type ConfiguredRow,
  type ConfirmedQuote,
} from './pricebook-api'
import { PricebookStateBadge } from './pricebook-state'

function ConfiguredCost(props: { rule: ConfiguredRow['cost_rule'] }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const rule = props.rule
  if (!rule) return <span>{t('No upstream costs configured')}</span>
  let prices
  if (rule.kind === 'token') {
    prices = (
      <>
        {t('Input')}: {formatProfitCny(rule.input_cost_per_million_cny, locale)}{' '}
        · {t('Cache Read')}:{' '}
        {formatProfitCny(rule.cache_cost_per_million_cny, locale)} ·{' '}
        {t('Output')}:{' '}
        {formatProfitCny(rule.output_cost_per_million_cny, locale)} /{' '}
        {t('Per million tokens')}
      </>
    )
  } else if (rule.resolution_costs_cny) {
    prices = (
      <>
        {Object.entries(rule.resolution_costs_cny).map(
          ([resolution, price]) => (
            <div key={resolution}>
              {resolution}: {formatProfitCny(price, locale)} /{' '}
              {t(rule.kind === 'h3_video' ? 'Per second' : 'Per image')}
            </div>
          )
        )}
        {rule.kind === 'h3_video' && (
          <div>
            {t('Free input images')}:{' '}
            {formatNumber(rule.free_input_images ?? 0, locale)} ·{' '}
            {t('Extra input image cost (CNY per image)')}:{' '}
            {formatProfitCny(rule.extra_image_cost_cny, locale)}
          </div>
        )}
      </>
    )
  } else {
    const units = {
      request: 'request',
      second: 'Per second',
      image: 'Per image',
    }
    const unit = units[rule.kind as keyof typeof units]
    prices = (
      <>
        {formatProfitCny(rule.unit_cost_cny, locale)} / {t(unit ?? 'Unknown')}
      </>
    )
  }
  return (
    <div className='space-y-1 text-xs'>
      <div>{prices}</div>
      {rule.kind === 'h3_video' && (
        <div>
          {t(
            rule.long_video_multiplier === 2
              ? '16–30 second output and reference material costs are doubled; 2K remains limited to 15 seconds.'
              : '4–15 second cost rule'
          )}
        </div>
      )}
      <div className='text-muted-foreground break-words'>{rule.source}</div>
      <div className='text-muted-foreground'>
        {t('Effective from (Shanghai time)')}:{' '}
        {shanghaiDateTime(rule.effective_from * 1000).replace('T', ' ')}
      </div>
      {rule.deepseek_peak_multiplier !== undefined && (
        <div>
          {t('DeepSeek peak multiplier')}:{' '}
          {formatNumber(rule.deepseek_peak_multiplier, locale)}
        </div>
      )}
    </div>
  )
}

function procurementQuoteLabel(basis: ConfirmedQuote['basis']) {
  if (basis === 'public_reference_estimate') {
    return 'Verified public reference estimate'
  }
  if (basis === 'user_quote_estimate') {
    return 'Verified user-confirmed procurement estimate'
  }
  return 'Confirmed procurement quote'
}

function ConfirmedQuoteValue(props: {
  quote: ConfirmedQuote
  field: 'procurement_cny' | 'retail_cny'
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const groups = new Map<
    string,
    { unit: string; direction: string; values: number[] }
  >()
  for (const price of props.quote.prices) {
    const key = `${price.unit}:${price.direction}`
    const group = groups.get(key) ?? {
      unit: price.unit,
      direction: price.direction,
      values: [],
    }
    group.values.push(price[props.field])
    groups.set(key, group)
  }
  let quoteLabel = t(procurementQuoteLabel(props.quote.basis))
  if (props.field === 'retail_cny') quoteLabel = t('Approved retail quote')
  return (
    <div className='space-y-1 text-xs'>
      {[...groups.entries()].map(([key, group]) => {
        const min = Math.min(...group.values),
          max = Math.max(...group.values)
        let unit = t('CNY / 1M tokens')
        if (group.unit === 'CNY / second') unit = t('CNY / second')
        else if (group.unit === 'CNY / image') unit = t('CNY / image')
        else if (!group.unit.includes('token')) unit = group.unit
        return (
          <div key={key}>
            {group.direction && (
              <span>
                {t(group.direction === 'input' ? 'Input' : 'Output')}:{' '}
              </span>
            )}
            <strong>
              {formatProfitCny(min, locale)}
              {max !== min && <> – {formatProfitCny(max, locale)}</>}
            </strong>
            <span> / {unit}</span>
          </div>
        )
      })}
      <div className='text-muted-foreground'>
        {quoteLabel} · {props.quote.checked_at}
      </div>
    </div>
  )
}
export function ConfiguredModelPricebook() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const user = useAuthStore((state) => state.auth.user)
  const query = useConfiguredPricebook()
  const [search, setSearch] = useState('')
  const [stateFilter, setStateFilter] = useState('all')
  const [groupDraft, setGroupDraft] = useState('default')
  const [pagination, setPagination] = useState({ pageIndex: 0, pageSize: 10 })
  const [details, setDetails] = useState<{
    kind: 'quote' | 'effective'
    row: ConfiguredRow
  } | null>(null)
  const rows = useMemo(
    () =>
      (query.data?.rows ?? []).filter((row) => {
        if (stateFilter === 'enabled' && row.state !== 'enabled') return false
        if (stateFilter === 'not_enabled' && row.state === 'enabled') {
          return false
        }
        return `${row.model_name} ${row.approved_quote?.name ?? ''} ${row.channel_name} ${row.upstream_host} ${row.upstream_host === 'metaso.cn' ? t('Metaso direct') : ''}`
          .toLowerCase()
          .includes(search.trim().toLowerCase())
      }),
    [query.data, search, stateFilter, t]
  )
  const groups = [
    ...new Set(
      (query.data?.rows ?? []).flatMap((row) =>
        row.configured_groups.length
          ? row.configured_groups
          : row.enabled_groups
      )
    ),
  ].sort()
  const selectedGroup = groups.includes(groupDraft) ? groupDraft : groups[0]
  const allStates = pricebookStatesByModel(query.data?.rows ?? [])
  const total = Object.keys(allStates).length,
    enabled = Object.values(allStates).filter(
      (state) => state === 'enabled'
    ).length
  const columns: ColumnDef<ConfiguredRow>[] = [
    {
      accessorKey: 'model_name',
      header: t('Model Name'),
      meta: { mobileTitle: true },
      cell: ({ row }) => (
        <div className='max-w-64 space-y-1 break-all'>
          <strong>
            {row.original.approved_quote?.name ?? row.original.model_name}
          </strong>
          {row.original.approved_quote &&
            row.original.approved_quote.name !== row.original.model_name && (
              <div className='text-muted-foreground text-xs'>
                {row.original.model_name}
              </div>
            )}
        </div>
      ),
    },
    {
      id: 'upstream',
      header: t('Upstream'),
      cell: ({ row }) => (
        <div className='space-y-1 text-xs'>
          <div>
            {row.original.upstream_host === 'metaso.cn'
              ? t('Metaso direct')
              : row.original.approved_quote?.provider}
            <div>{row.original.upstream_host}</div>
          </div>
          <div>
            {row.original.channel_id > 0
              ? `#${row.original.channel_id} · ${row.original.channel_name}`
              : t('Not connected to a channel')}
          </div>
        </div>
      ),
    },
    {
      id: 'status',
      header: t('Status'),
      cell: ({ row }) => (
        <div className='space-y-1'>
          <PricebookStateBadge state={row.original.state} />
          {row.original.channel_status === 3 && (
            <div className='text-muted-foreground text-xs'>
              {t('Automatically disabled')}
            </div>
          )}
          <div className='text-muted-foreground text-xs'>
            {row.original.enabled_groups.join(', ')}
          </div>
        </div>
      ),
    },
    {
      id: 'retail',
      header: t('Retail price'),
      cell: ({ row }) => {
        if (row.original.pricing) {
          return (
            <div>
              <ModelPriceCell
                model={{
                  ...row.original.pricing,
                  group_ratio: query.data?.group_ratios,
                }}
                options={{
                  tokenUnit: 'M',
                  usdExchangeRate: query.data?.usd_exchange_rate,
                  selectedGroup,
                }}
              />
              <div className='text-muted-foreground text-xs'>
                {t('Current configuration')}
              </div>
            </div>
          )
        }
        if (row.original.approved_quote) {
          return (
            <ConfirmedQuoteValue
              quote={row.original.approved_quote}
              field='retail_cny'
            />
          )
        }
        return <span>{t('Pricing is unavailable')}</span>
      },
    },
    {
      id: 'cost',
      header: t('Procurement price'),
      cell: ({ row }) => {
        let value = <span>{t('No confirmed quote recorded')}</span>
        if (row.original.approved_quote) {
          value = (
            <ConfirmedQuoteValue
              quote={row.original.approved_quote}
              field='procurement_cny'
            />
          )
        } else if (row.original.cost_rule) {
          value = <ConfiguredCost rule={row.original.cost_rule} />
        }
        return (
          <div className='space-y-1'>
            {value}
            {row.original.approved_quote && (
              <Button
                type='button'
                variant='link'
                size='sm'
                className='h-auto px-0 text-xs'
                onClick={() => setDetails({ kind: 'quote', row: row.original })}
              >
                {t('View confirmed specification prices')}
              </Button>
            )}
          </div>
        )
      },
    },
    {
      id: 'effective',
      header: t('Cost rule status'),
      cell: ({ row }) => (
        <div className='text-xs'>
          {row.original.cost_rule ? (
            <>
              <Badge variant='secondary'>
                {t('Effective cost rule exists')}
              </Badge>
              <Button
                type='button'
                variant='link'
                size='sm'
                className='h-auto px-0 text-xs'
                onClick={() =>
                  setDetails({ kind: 'effective', row: row.original })
                }
              >
                {t('View effective cost rule')}
              </Button>
            </>
          ) : (
            <span>{t('Not yet effective')}</span>
          )}
        </div>
      ),
    },
    {
      id: 'expression',
      header: t('Billing expression'),
      cell: ({ row }) => (
        <div className='max-w-sm space-y-1 text-xs break-all'>
          {Object.entries(row.original.expressions).map(([key, expression]) => (
            <div key={key}>
              {key}: {expression}
            </div>
          ))}
        </div>
      ),
    },
  ]
  const { table } = useDataTable({
    data: rows,
    columns,
    enableRowSelection: false,
    enableSorting: false,
    pagination,
    onPaginationChange: setPagination,
    getRowId: (row) => `${row.channel_id}:${row.model_name}`,
  })
  if (!user || user.role < ROLE.SUPER_ADMIN) {
    return <ErrorState title={t('Access denied')} />
  }
  if (query.isPending) {
    return <LoadingState message={t('Loading configured model prices')} />
  }
  if (query.isError) {
    return (
      <ErrorState
        title={t('Failed to load configured model prices')}
        onRetry={() => void query.refetch()}
      />
    )
  }
  return (
    <section aria-label={t('Current configured models')} className='space-y-3'>
      <h2 className='text-lg font-semibold'>
        {t('All configured and planned models')}
      </h2>
      <div className='flex flex-wrap gap-2'>
        <Badge variant='outline'>
          {t('Total models: {{count}}', { count: total })}
        </Badge>
        <Badge variant='secondary'>
          {t('Enabled models: {{count}}', { count: enabled })}
        </Badge>
        <Badge variant='outline'>
          {t('Not enabled models: {{count}}', { count: total - enabled })}
        </Badge>
      </div>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Confirmed quotes remain visible for disabled and planned models. Model activation and cost rule effectiveness are shown separately. Status follows channel and group switches; retail prices use base group ratios and the current display currency.'
        )}
      </p>
      <DataTablePage
        table={table}
        columns={columns}
        fixedHeight={false}
        paginationInFooter={false}
        emptyTitle={t('No matching prices')}
        tableClassName='[&_table]:min-w-[1280px]'
        toolbar={
          <div className='flex flex-wrap items-end gap-3'>
            <label className='block max-w-lg flex-1 space-y-1 text-sm'>
              <span>{t('Search configured model or upstream')}</span>
              <Input
                value={search}
                maxLength={256}
                onChange={(event) => {
                  setSearch(event.target.value)
                  setPagination((previous) => ({ ...previous, pageIndex: 0 }))
                }}
              />
            </label>
            <label className='space-y-1 text-sm'>
              <span>{t('Status')}</span>
              <NativeSelect
                aria-label={t('Model activation status')}
                value={stateFilter}
                onChange={(event) => {
                  setStateFilter(event.target.value)
                  setPagination((previous) => ({ ...previous, pageIndex: 0 }))
                }}
              >
                <NativeSelectOption value='all'>{t('All')}</NativeSelectOption>
                <NativeSelectOption value='enabled'>
                  {t('Enabled')}
                </NativeSelectOption>
                <NativeSelectOption value='not_enabled'>
                  {t('Not enabled')}
                </NativeSelectOption>
              </NativeSelect>
            </label>
            <label className='space-y-1 text-sm'>
              <span>{t('Price group')}</span>
              <NativeSelect
                aria-label={t('Price group')}
                value={selectedGroup ?? ''}
                disabled={!groups.length}
                onChange={(event) => setGroupDraft(event.target.value)}
              >
                {groups.map((group) => (
                  <NativeSelectOption key={group} value={group}>
                    {group}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </label>
            <Button
              type='button'
              variant='outline'
              disabled={query.isFetching}
              onClick={() => void query.refetch()}
            >
              {t('Refresh')}
            </Button>
          </div>
        }
      />
      <Dialog
        open={details !== null}
        onOpenChange={(open) => {
          if (!open) setDetails(null)
        }}
        title={
          details?.row.approved_quote?.name ??
          details?.row.model_name ??
          t('Procurement price')
        }
        description={t(
          details?.kind === 'effective'
            ? 'View effective cost rule'
            : 'View confirmed specification prices'
        )}
      >
        {details?.kind === 'effective' && (
          <ConfiguredCost rule={details.row.cost_rule} />
        )}
        {details?.kind === 'quote' && details.row.approved_quote && (
          <div className='space-y-3'>
            <p className='text-xs break-words'>
              {details.row.approved_quote.source}
            </p>
            <p className='text-muted-foreground text-xs'>
              {t(procurementQuoteLabel(details.row.approved_quote.basis))} ·{' '}
              {details.row.approved_quote.checked_at}
            </p>
            <StaticDataTable
              data={details.row.approved_quote.prices}
              getRowKey={(_row, index) => String(index)}
              columns={[
                {
                  id: 'params',
                  header: t('Parameters'),
                  cell: (row) => (
                    <div className='text-xs break-words'>
                      {row.direction &&
                        t(row.direction === 'input' ? 'Input' : 'Output')}
                      {Object.entries(row.params).map(([key, value]) => (
                        <div key={key}>
                          {key}: {String(value)}
                        </div>
                      ))}
                    </div>
                  ),
                },
                { id: 'unit', header: t('Unit'), cell: (row) => row.unit },
                {
                  id: 'cost',
                  header: t('Procurement price'),
                  cell: (row) => formatProfitCny(row.procurement_cny, locale),
                },
                {
                  id: 'retail',
                  header: t('Retail price'),
                  cell: (row) => formatProfitCny(row.retail_cny, locale),
                },
              ]}
            />
          </div>
        )}
      </Dialog>
    </section>
  )
}
