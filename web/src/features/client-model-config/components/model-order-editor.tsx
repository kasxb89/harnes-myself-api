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
import { Reorder } from 'motion/react'
import { useTranslation } from 'react-i18next'

import { AutoGroupOrderItem } from '@/components/auto-group-order-item'
import { EmptyState } from '@/components/empty-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import { MAX_CLIENT_MODEL_ORDER } from '../lib/schema'
import type { ClientModelOption } from '../types'

type ModelOrderEditorProps = {
  value: string[]
  models: ClientModelOption[]
  defaultChatModel: string
  disabled: boolean
  onChange: (value: string[]) => void
}

export function ModelOrderEditor(props: ModelOrderEditorProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const candidates = props.models
    .filter((model) => !props.value.includes(model.id))
    .map((model) => ({
      value: model.id,
      label: model.id,
      description:
        model.display_name !== model.id ? model.display_name : undefined,
    }))
  const atLimit = props.value.length >= MAX_CLIENT_MODEL_ORDER

  const moveModel = (index: number, direction: 'up' | 'down'): void => {
    const target = direction === 'up' ? index - 1 : index + 1
    if (props.disabled || target < 0 || target >= props.value.length) return
    const next = [...props.value]
    ;[next[index], next[target]] = [next[target], next[index]]
    props.onChange(next)
  }

  return (
    <div className='min-w-0 space-y-3'>
      <div className='flex flex-wrap items-center justify-between gap-2'>
        <p className='text-muted-foreground text-sm' aria-live='polite'>
          {t('{{count}} prioritized models', {
            count: formatNumber(props.value.length, locale),
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
      <Combobox
        aria-label={t('Add prioritized model')}
        options={candidates}
        value={null}
        onValueChange={(id: string | null) => {
          if (id && !props.disabled && !atLimit && !props.value.includes(id)) {
            props.onChange([...props.value, id])
          }
        }}
        placeholder={t('Add prioritized model')}
        emptyText={t('No more available models')}
        disabled={props.disabled || atLimit || candidates.length === 0}
        className='w-full'
      />
      {props.value.length === 0 ? (
        <EmptyState
          title={t('No model order preference')}
          description={t(
            'Models keep their catalog order until you add a priority.'
          )}
          className='min-h-32 border'
        />
      ) : (
        <Reorder.Group
          axis='y'
          values={props.value}
          aria-label={t('Client model order')}
          onReorder={(ids) => {
            if (!props.disabled) props.onChange(ids)
          }}
          className='flex min-w-0 flex-col gap-2'
        >
          {props.value.map((id, index) => (
            <AutoGroupOrderItem
              key={id}
              group={id}
              index={index}
              count={props.value.length}
              leading={
                <span className='text-muted-foreground text-xs tabular-nums'>
                  {formatNumber(index + 1, locale)}
                </span>
              }
              disabled={props.disabled}
              labels={{
                drag: t('Drag {{model}} to reorder', { model: id }),
                moveUp: t('Move {{model}} up', { model: id }),
                moveDown: t('Move {{model}} down', { model: id }),
                remove: t('Remove {{model}} from priority', { model: id }),
              }}
              onMove={moveModel}
              onRemove={(model) =>
                props.onChange(props.value.filter((value) => value !== model))
              }
            >
              {id === props.defaultChatModel && (
                <Badge variant='secondary'>{t('Default chat model')}</Badge>
              )}
              {!props.models.some((model) => model.id === id) && (
                <Badge variant='outline'>{t('Unavailable')}</Badge>
              )}
            </AutoGroupOrderItem>
          ))}
        </Reorder.Group>
      )}
    </div>
  )
}
