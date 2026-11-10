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
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { MultiSelect } from '@/components/multi-select'
import { Checkbox } from '@/components/ui/checkbox'
import { Combobox } from '@/components/ui/combobox'
import { Field, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'

import {
  MEDIA_PARAMETERS,
  parameterSeed,
  mediaParameterErrors,
  type MediaParameterField,
  type MediaParameterName,
} from '../lib/media-parameters'

function IntegerConstraintInput(props: {
  id: string
  label: string
  value?: number
  disabled?: boolean
  onChange: (value?: number) => void
}) {
  const { t } = useTranslation()
  const [draft, setDraft] = useState<{ source?: number; text: string } | null>(
    null
  )
  const text =
    draft && draft.source === props.value
      ? draft.text
      : String(props.value ?? '')
  const invalid =
    !props.disabled &&
    text !== '' &&
    (!/^-?\d+$/.test(text) || !Number.isSafeInteger(Number(text)))
  return (
    <div className='space-y-1' data-generation-invalid={invalid || undefined}>
      <Input
        id={props.id}
        aria-label={props.label}
        type='text'
        inputMode='numeric'
        value={text}
        disabled={props.disabled}
        aria-invalid={invalid || undefined}
        onChange={(event) => {
          const next = event.target.value
          if (
            next === '' ||
            (/^-?\d+$/.test(next) && Number.isSafeInteger(Number(next)))
          ) {
            const value = next === '' ? undefined : Number(next)
            setDraft({ source: value, text: next })
            props.onChange(value)
          } else setDraft({ source: props.value, text: next })
        }}
      />
      {invalid && (
        <p role='alert' className='text-destructive text-sm'>
          {t('Enter a finite whole number.')}
        </p>
      )}
    </div>
  )
}

export function MediaParameterFieldEditor(props: {
  name: MediaParameterName
  field?: MediaParameterField | false
  nativeField?: MediaParameterField | false
  required: boolean
  canChangeRequired: boolean
  disabled?: boolean
  branch?: boolean
  showName?: boolean
  onChange: (field: MediaParameterField | false | undefined) => void
  onRequiredChange: (value: boolean) => void
}) {
  const { t } = useTranslation()
  const id = useId()
  const [type, labelKey] = MEDIA_PARAMETERS[props.name]
  const label = props.showName ? `${t(labelKey)} (${props.name})` : t(labelKey)
  let mode = 'custom'
  if (props.field === undefined) mode = 'inherit'
  else if (props.field === false) mode = 'disabled'
  const field = props.field ?? props.nativeField
  const active = mode === 'custom' && !props.disabled
  const patch = (key: string, value: unknown) => {
    const next = parameterSeed(props.name, props.field ?? props.nativeField)
    // Keep future/unrecognized constraints on an already editable declaration.
    if (props.field) Object.assign(next, props.field)
    if (value === undefined) delete next[key]
    else next[key] = value
    if (
      (key === 'minimum' || key === 'maximum') &&
      type === 'integer' &&
      next.enum
    ) {
      next.enum = next.enum.filter(
        (item) =>
          typeof item !== 'number' ||
          ((next.minimum === undefined || item >= next.minimum) &&
            (next.maximum === undefined || item <= next.maximum))
      )
    }
    props.onChange(next)
  }
  const choiceLabel = (value: unknown) => {
    if (typeof value === 'boolean') return t(value ? 'Yes' : 'No')
    return String(value)
  }
  const choiceValue = (value: string) => {
    if (type === 'integer' && /^-?\d+$/.test(value)) return Number(value)
    if (type === 'boolean') return value === 'true'
    return value
  }
  const scalarControl = (key: 'default' | 'const') => {
    const controlId = `${id}-${key}`
    const controlLabel = t(
      key === 'default'
        ? 'Default value for {{parameter}}'
        : 'Fixed value for {{parameter}}',
      { parameter: label }
    )
    const value = field && Object.hasOwn(field, key) ? field[key] : undefined
    if (type === 'array') {
      return (
        <MultiSelect
          id={controlId}
          options={(Array.isArray(value) ? value : [])
            .filter((item): item is string => typeof item === 'string')
            .map((item) => ({ value: item, label: item }))}
          selected={(Array.isArray(value) ? value : []).filter(
            (item): item is string => typeof item === 'string'
          )}
          maxVisibleChips={4}
          allowCreate
          disabled={!active}
          onChange={(values) => patch(key, values.length ? values : undefined)}
        />
      )
    }
    if (type === 'boolean' || (field && field.enum?.length)) {
      const menu =
        (field && field.enum?.length ? field.enum : undefined) ??
        (type === 'boolean' ? [true, false] : [])
      return (
        <Combobox
          id={controlId}
          aria-label={controlLabel}
          value={value === undefined ? '__unset__' : JSON.stringify(value)}
          options={[
            { value: '__unset__', label: t('Not overridden') },
            ...menu.map((item) => ({
              value: JSON.stringify(item),
              label: choiceLabel(item),
            })),
          ]}
          disabled={!active}
          onValueChange={(choice) => {
            if (choice !== null) {
              patch(
                key,
                choice === '__unset__' ? undefined : JSON.parse(choice)
              )
            }
          }}
        />
      )
    }
    if (type === 'integer') {
      return (
        <IntegerConstraintInput
          id={controlId}
          label={controlLabel}
          value={typeof value === 'number' ? value : undefined}
          disabled={!active}
          onChange={(number) => patch(key, number)}
        />
      )
    }
    return (
      <Input
        id={controlId}
        aria-label={controlLabel}
        value={typeof value === 'string' ? value : ''}
        disabled={!active}
        onChange={(event) => patch(key, event.target.value || undefined)}
      />
    )
  }
  const fieldErrors =
    active && props.field
      ? mediaParameterErrors({ properties: { [props.name]: props.field } })
      : []
  return (
    <fieldset
      data-generation-invalid={fieldErrors.length > 0 || undefined}
      className='min-w-0 space-y-3 border-b py-4'
      disabled={props.disabled}
    >
      <legend className='sr-only'>{label}</legend>
      <div className='grid items-center gap-3 sm:grid-cols-[minmax(0,1fr)_14rem]'>
        <div className='flex flex-wrap items-center gap-3'>
          <h4 className='font-medium'>{label}</h4>
          <label className='flex items-center gap-2 text-sm'>
            <Checkbox
              checked={props.required}
              disabled={!active || !props.canChangeRequired}
              aria-label={t('Require {{parameter}}', { parameter: label })}
              onCheckedChange={(value) =>
                props.onRequiredChange(value === true)
              }
            />
            {t('Required')}
          </label>
        </div>
        <Combobox
          aria-label={t('Settings for {{parameter}}', { parameter: label })}
          value={mode}
          options={[
            {
              value: 'inherit',
              label: t(
                props.branch ? 'No extra constraint' : 'Use adapter settings'
              ),
            },
            { value: 'custom', label: t('Customize') },
            {
              value: 'disabled',
              label: t('Disabled'),
              disabled: props.required && !props.canChangeRequired,
            },
          ]}
          disabled={props.disabled}
          onValueChange={(value) => {
            if (value === mode) return
            if (value === 'inherit') props.onChange(undefined)
            else if (value === 'disabled') props.onChange(false)
            else if (value === 'custom') {
              props.onChange(parameterSeed(props.name, props.nativeField))
            }
          }}
        />
      </div>
      {field && (
        <div className='grid min-w-0 gap-3 sm:grid-cols-2 xl:grid-cols-4'>
          {type !== 'array' && (
            <Field className='min-w-0 gap-2'>
              <FieldLabel htmlFor={`${id}-choices`}>
                {t('Allowed values for {{parameter}}', { parameter: label })}
              </FieldLabel>
              <MultiSelect
                id={`${id}-choices`}
                options={[
                  ...((props.nativeField && props.nativeField.enum) || []),
                  ...(field.enum || []),
                  ...(type === 'boolean' ? [true, false] : []),
                ]
                  .filter(
                    (item, index, array) =>
                      array.findIndex(
                        (candidate) =>
                          JSON.stringify(candidate) === JSON.stringify(item)
                      ) === index
                  )
                  .map((item) => ({
                    value: String(item),
                    label: choiceLabel(item),
                  }))}
                selected={(field.enum || []).map(String)}
                maxVisibleChips={4}
                allowCreate={type !== 'boolean'}
                disabled={!active}
                onChange={(values) =>
                  patch(
                    'enum',
                    values.length ? values.map(choiceValue) : undefined
                  )
                }
              />
            </Field>
          )}
          {(type === 'integer' || type === 'array') &&
            (type === 'array'
              ? ['minItems', 'maxItems']
              : ['minimum', 'maximum']
            ).map((bound) => {
              const lower = bound === 'minimum' || bound === 'minItems'
              const controlLabel = t(
                lower
                  ? 'Minimum for {{parameter}}'
                  : 'Maximum for {{parameter}}',
                { parameter: label }
              )
              const value = field[bound]
              return (
                <Field className='min-w-0 gap-2' key={bound}>
                  <FieldLabel htmlFor={`${id}-${bound}`}>
                    {controlLabel}
                  </FieldLabel>
                  <IntegerConstraintInput
                    id={`${id}-${bound}`}
                    label={controlLabel}
                    value={typeof value === 'number' ? value : undefined}
                    disabled={!active}
                    onChange={(number) => patch(bound, number)}
                  />
                </Field>
              )
            })}
          {(['default', 'const'] as const)
            .filter((key) => type !== 'array' || Object.hasOwn(field, key))
            .map((key) => (
              <Field className='min-w-0 gap-2' key={key}>
                <FieldLabel htmlFor={`${id}-${key}`}>
                  {t(
                    key === 'default'
                      ? 'Default value for {{parameter}}'
                      : 'Fixed value for {{parameter}}',
                    { parameter: label }
                  )}
                </FieldLabel>
                {scalarControl(key)}
              </Field>
            ))}
        </div>
      )}
      {fieldErrors.map((error) => (
        <p key={error} role='alert' className='text-destructive text-sm'>
          {label}: {t(error)}
        </p>
      ))}
      {mode === 'inherit' && (
        <p className='text-muted-foreground text-sm'>
          {t(
            'Adapter values are shown. Choose Customize to edit this parameter.'
          )}
        </p>
      )}
      {mode === 'custom' && (
        <p className='text-muted-foreground text-sm'>
          {t(
            'Leave a constraint blank to keep the adapter constraint. Other parameters stay unchanged.'
          )}
        </p>
      )}
    </fieldset>
  )
}
