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
import { useId } from 'react'
import { useTranslation } from 'react-i18next'

import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Field, FieldGroup } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import {
  clientModelRankSchema,
  MAX_CLIENT_MODEL_ORDER,
  type ClientModelConfigFormValues,
} from '../lib/schema'
import type { ClientModelOption } from '../types'

type ModelOrderEditorProps = {
  value: ClientModelConfigFormValues['model_ranks']
  models: ClientModelOption[]
  defaultChatModel: string
  disabled: boolean
  onChange: (value: ClientModelConfigFormValues['model_ranks']) => void
}

type ModelRow = { id: string; displayName: string; available: boolean }

export function ModelOrderEditor(props: ModelOrderEditorProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const editorId = useId()
  const ranks = new Map(props.value.map((entry) => [entry.id, entry.rank]))
  const catalogIds = new Set(props.models.map((model) => model.id))
  const rows: ModelRow[] = [
    ...props.models.map((model) => ({
      id: model.id,
      displayName: model.display_name,
      available: true,
    })),
    ...props.value
      .filter((entry) => !catalogIds.has(entry.id))
      .map((entry) => ({
        id: entry.id,
        displayName: entry.id,
        available: false,
      })),
  ]
  const prioritizedCount = props.value.filter(
    (entry) => entry.rank !== ''
  ).length
  const atLimit = prioritizedCount >= MAX_CLIENT_MODEL_ORDER
  const columns: StaticDataTableColumn<ModelRow>[] = [
    {
      id: 'model',
      header: t('Model'),
      cellClassName: 'whitespace-normal',
      cell: (row) => (
        <div className='flex min-w-0 flex-col gap-1'>
          <span className='break-all'>{row.id}</span>
          {row.displayName !== row.id && (
            <span className='text-muted-foreground break-words'>
              {row.displayName}
            </span>
          )}
          <div className='flex flex-wrap gap-1'>
            {row.id === props.defaultChatModel && (
              <Badge
                variant='secondary'
                className='h-auto max-w-full text-left whitespace-normal'
              >
                {t('Default chat model')}
              </Badge>
            )}
            {!row.available && (
              <Badge
                variant='outline'
                className='h-auto max-w-full text-left whitespace-normal'
              >
                {t('Unavailable')}
              </Badge>
            )}
          </div>
        </div>
      ),
    },
    {
      id: 'rank',
      header: t('Sort number'),
      className: 'w-36',
      cellClassName: 'whitespace-normal',
      cell: (row, index) => {
        const rank = ranks.get(row.id) ?? ''
        const invalid = !clientModelRankSchema.safeParse(rank).success
        const errorId = `${editorId}-${index}-error`
        return (
          <FieldGroup>
            <Field
              data-invalid={invalid || undefined}
              data-disabled={props.disabled || undefined}
            >
              <Input
                type='text'
                inputMode='numeric'
                autoComplete='off'
                aria-label={t('Sort number for {{model}}', { model: row.id })}
                aria-invalid={invalid}
                aria-describedby={invalid ? errorId : undefined}
                placeholder={t('Unranked')}
                value={rank}
                disabled={props.disabled || (atLimit && rank === '')}
                onChange={(event) => {
                  const nextRank = event.target.value
                  const next = props.value.filter(
                    (entry) => entry.id !== row.id
                  )
                  if (nextRank !== '') next.push({ id: row.id, rank: nextRank })
                  props.onChange(next)
                }}
              />
              {invalid && (
                <p id={errorId} className='text-destructive text-xs'>
                  {t('Enter a whole number from 1 to 999999, or leave blank.')}
                </p>
              )}
              {!row.available && (
                <Button
                  type='button'
                  variant='outline'
                  size='sm'
                  className='h-auto whitespace-normal'
                  aria-label={t('Remove {{model}} from priority', {
                    model: row.id,
                  })}
                  disabled={props.disabled}
                  onClick={() =>
                    props.onChange(
                      props.value.filter((entry) => entry.id !== row.id)
                    )
                  }
                >
                  {t('Remove')}
                </Button>
              )}
            </Field>
          </FieldGroup>
        )
      },
    },
  ]

  return (
    <div className='flex min-w-0 flex-col gap-3'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <p className='text-muted-foreground text-sm' aria-live='polite'>
          {t('{{count}} prioritized models', {
            count: formatNumber(prioritizedCount, locale),
          })}
        </p>
        <Button
          type='button'
          variant='outline'
          size='sm'
          disabled={props.disabled || props.value.length === 0}
          onClick={() => props.onChange([])}
        >
          {t('Use catalog order')}
        </Button>
      </div>
      {atLimit && (
        <p className='text-muted-foreground text-sm'>
          {t('You can prioritize up to 256 models.')}
        </p>
      )}
      <StaticDataTable
        columns={columns}
        data={rows}
        getRowKey={(row) => row.id}
        tableProps={{ 'aria-label': t('Client model order') }}
        tableClassName='table-fixed'
        emptyContent={t('No available models')}
      />
      <p className='text-muted-foreground text-sm'>
        {t(
          'Sort numbers are saved as model order and shown as consecutive numbers after saving.'
        )}
      </p>
    </div>
  )
}
