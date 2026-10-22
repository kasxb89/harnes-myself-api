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
import { ComboboxInput } from '@/components/ui/combobox-input'
import { Field, FieldLabel } from '@/components/ui/field'
import { CompactDateTimeRangePicker } from '@/features/usage-logs/components/compact-date-time-range-picker'

export interface MediaFiltersDraft {
  start?: Date
  end?: Date
  kind: 'all' | 'image' | 'video'
  model: string
}

export function MediaOperationsFilters(props: {
  value: MediaFiltersDraft
  models: string[]
  loading: boolean
  invalid: boolean
  onChange: (value: MediaFiltersDraft) => void
  onApply: () => void
  onReset: () => void
}) {
  const { t } = useTranslation()
  return (
    <form
      aria-label={t('Media operations filters')}
      className='grid items-end gap-3 rounded-xl border p-4 sm:grid-cols-2 xl:grid-cols-[2fr_1fr_2fr_auto]'
      onSubmit={(event) => {
        event.preventDefault()
        if (!props.invalid && !props.loading) props.onApply()
      }}
    >
      <Field aria-label={t('History period')}>
        <FieldLabel>{t('History period')}</FieldLabel>
        <CompactDateTimeRangePicker
          start={props.value.start}
          end={props.value.end}
          onChange={(range) =>
            props.onChange({
              ...props.value,
              start: range.start,
              end: range.end
                ? new Date(Math.min(Date.now(), range.end.getTime()))
                : undefined,
            })
          }
        />
      </Field>
      <Field>
        <FieldLabel htmlFor='media-operation-kind'>
          {t('Media type')}
        </FieldLabel>
        <ComboboxInput
          allowCustomValue={false}
          openOnFocus={false}
          id='media-operation-kind'
          aria-label={t('Media type')}
          value={props.value.kind}
          options={[
            { value: 'all', label: t('Images and videos') },
            { value: 'image', label: t('Image') },
            { value: 'video', label: t('Video') },
          ]}
          onValueChange={(value) => {
            if (value === 'all' || value === 'image' || value === 'video') {
              props.onChange({ ...props.value, kind: value })
            }
          }}
        />
      </Field>
      <Field>
        <FieldLabel htmlFor='media-operation-model'>{t('Model')}</FieldLabel>
        <ComboboxInput
          allowCustomValue={false}
          openOnFocus={false}
          id='media-operation-model'
          aria-label={t('Model')}
          value={props.value.model}
          options={[
            { value: '', label: t('All models') },
            ...props.models.map((model) => ({ value: model, label: model })),
          ]}
          placeholder={t('Search models...')}
          emptyText={t('No matching models')}
          onValueChange={(model) => {
            if (model !== null) props.onChange({ ...props.value, model })
          }}
        />
      </Field>
      <div className='flex gap-2'>
        <Button type='submit' disabled={props.invalid || props.loading}>
          {t('Apply filters')}
        </Button>
        <Button type='button' variant='outline' onClick={props.onReset}>
          {t('Reset')}
        </Button>
      </div>
    </form>
  )
}
