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
import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { formatCatalogCny } from './price-format'
import type { YcapiModel } from './types'

type PriceRow = NonNullable<YcapiModel['priceRows']>[number]
export function YcapiModelPrices(props: { model: YcapiModel }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const rows = props.model.priceRows || []
  if (!rows.length) return null
  let unit = t('CNY / 1M tokens')
  if (props.model.kind === 'image') unit = t('CNY / image')
  if (props.model.kind === 'video') unit = t('CNY / second')
  const priceCell = (
    row: PriceRow,
    field: 'procurementCny' | 'retailCny' | 'officialCny',
    totalField: 'procurementTotalCny' | 'retailTotalCny' | 'officialTotalCny'
  ) => (
    <div>
      {formatCatalogCny(row[field], locale)}
      {props.model.kind === 'video' && row[totalField] !== undefined && (
        <div className='text-muted-foreground text-xs'>
          {t('Native clip total')}: {formatCatalogCny(row[totalField], locale)}
        </div>
      )}
    </div>
  )
  const labels: Record<string, string> = {
    quality: t('Quality'),
    resolution: t('Resolution'),
    size: t('Size'),
    duration: t('Duration'),
    aspect_ratio: t('Aspect ratio'),
    count: t('Count'),
    voice: t('Voice'),
    response_format: t('Format'),
  }
  const columns: StaticDataTableColumn<PriceRow>[] = [
    {
      id: 'parameters',
      header: t('Parameters'),
      className: 'min-w-44',
      cell: (row) => (
        <div className='space-y-1 text-xs'>
          {row.direction && (
            <strong>
              {row.direction === 'input' ? t('Input') : t('Output')}
            </strong>
          )}
          {Object.entries(row.params).map(([key, value]) => (
            <div key={key}>
              {labels[key] || key}: {String(value)}
            </div>
          ))}
        </div>
      ),
    },
    { id: 'unit', header: t('Unit'), cell: () => unit },
    {
      id: 'cost',
      header: t('Procurement price'),
      cell: (row) => priceCell(row, 'procurementCny', 'procurementTotalCny'),
    },
    {
      id: 'retail',
      header: t('Retail price'),
      cell: (row) => priceCell(row, 'retailCny', 'retailTotalCny'),
    },
    {
      id: 'official',
      header: t('Official benchmark'),
      cell: (row) => (
        <a href={row.source} target='_blank' rel='noreferrer'>
          {priceCell(row, 'officialCny', 'officialTotalCny')}
        </a>
      ),
    },
    {
      id: 'cost-discount',
      header: t('Procurement discount'),
      cell: (row) =>
        t('{{value}} / 10 of official price', {
          value: formatNumber(row.procurementZhe, locale),
        }),
    },
    {
      id: 'retail-discount',
      header: t('Retail discount'),
      cell: (row) =>
        t('{{value}} / 10 of official price', {
          value: formatNumber(row.retailZhe, locale),
        }),
    },
    {
      id: 'lane',
      header: t('Pinned channel'),
      className: 'min-w-48',
      cell: (row) => (
        <div className='flex items-center gap-1'>
          <code className='max-w-56 text-xs break-all'>{row.laneId}</code>
          <CopyButton
            value={row.laneId}
            aria-label={t('Copy pinned channel')}
          />
        </div>
      ),
    },
  ]
  return (
    <section aria-label={t('Price list by parameters')} className='space-y-3'>
      <h3 className='font-medium'>{t('Price list by parameters')}</h3>
      <StaticDataTable
        columns={columns}
        data={rows}
        getRowKey={(row) => row.quoteId || row.direction || row.modelId}
      />
      {[
        ...new Set(rows.map((row) => row.officialConditions).filter(Boolean)),
      ].map((note) => (
        <p key={note} className='text-muted-foreground text-xs'>
          {note}
        </p>
      ))}
      <p className='text-muted-foreground text-xs'>
        {t('Verified at')}: {rows[0].rateVerifiedAt}
      </p>
    </section>
  )
}
