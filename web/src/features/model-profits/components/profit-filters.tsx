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

import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'

import {
  profitPreset,
  type ProfitPeriod,
  type ProfitPreset,
} from '../lib/period'

export function ProfitFilters(props: {
  value: ProfitPeriod
  onChange: (value: ProfitPeriod) => void
  onApply: () => void
  loading: boolean
  invalid: boolean
  models: string[]
}) {
  const { t } = useTranslation()
  const presets: { value: ProfitPreset; label: string }[] = [
    { value: 'today', label: t('Today') },
    { value: 'yesterday', label: t('Yesterday') },
    { value: 'week', label: t('Last 7 days') },
    { value: 'month', label: t('Month to date') },
  ]
  return (
    <div className='flex flex-col gap-3 rounded-xl border p-4'>
      <div className='flex flex-wrap items-center gap-2'>
        {presets.map((preset) => (
          <Button
            key={preset.value}
            size='sm'
            variant='outline'
            onClick={() =>
              props.onChange({
                ...profitPreset(preset.value),
                model: props.value.model,
              })
            }
          >
            {preset.label}
          </Button>
        ))}
        <span className='text-muted-foreground text-xs'>
          {t('Calendar dates in Asia/Shanghai (UTC+8)')}
        </span>
      </div>
      <form
        onSubmit={(event) => {
          event.preventDefault()
          props.onApply()
        }}
      >
        <FieldGroup className='grid items-end gap-3 sm:grid-cols-2 xl:grid-cols-[1fr_1fr_2fr_auto]'>
          <Field>
            <FieldLabel htmlFor='model-profit-start'>
              {t('Start date')}
            </FieldLabel>
            <Input
              id='model-profit-start'
              type='date'
              value={props.value.start}
              aria-invalid={props.invalid}
              onChange={(event) =>
                props.onChange({ ...props.value, start: event.target.value })
              }
            />
          </Field>
          <Field>
            <FieldLabel htmlFor='model-profit-end'>
              {t('End date (inclusive)')}
            </FieldLabel>
            <Input
              id='model-profit-end'
              type='date'
              value={props.value.end}
              aria-invalid={props.invalid}
              onChange={(event) =>
                props.onChange({ ...props.value, end: event.target.value })
              }
            />
          </Field>
          <Field>
            <FieldLabel htmlFor='model-profit-model'>{t('Model')}</FieldLabel>
            <Combobox
              id='model-profit-model'
              value={props.value.model}
              options={[
                { value: '', label: t('All models') },
                ...props.models.map((model) => ({
                  value: model,
                  label: model,
                })),
              ]}
              placeholder={t('All models')}
              searchPlaceholder={t('Search models...')}
              emptyText={t('No matching models')}
              aria-label={t('Model')}
              onValueChange={(model) =>
                props.onChange({ ...props.value, model: model ?? '' })
              }
            />
          </Field>
          <Button type='submit' disabled={props.loading || props.invalid}>
            {t('Apply filters')}
          </Button>
        </FieldGroup>
      </form>
    </div>
  )
}
