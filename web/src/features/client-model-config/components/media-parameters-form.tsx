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
import { useId, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'

import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import {
  Table,
  TableHead,
  TableHeader,
  TableBody,
  TableRow,
  TableCell,
} from '@/components/ui/table'

import {
  MEDIA_PARAMETERS,
  isMediaParameterName,
  readMediaParameters,
  nativeMediaFields,
  parameterSeed,
  updateMediaField,
  updateMediaRequired,
  importMediaCombinations,
  serializeMediaParameters,
  mediaParameterErrors,
  type MediaParameterDocument,
  type MediaParameterField,
  type MediaParameterObject,
} from '../lib/media-parameters'
import { MediaParameterFieldEditor } from './media-parameter-field'

export function MediaParametersForm(props: {
  value: string
  nativeSchema?: Record<string, unknown>
  disabled?: boolean
  onChange: (value: string) => void
}) {
  const { t } = useTranslation()
  const combinationPrefix = useId()
  const combinationIds = useRef<string[]>([])
  const nextCombinationId = useRef(0)
  const [selectedCombination, setSelectedCombination] = useState(0)
  const document = readMediaParameters(props.value)
  const parsedNative = readMediaParameters(props.nativeSchema)
  const native = parsedNative ?? {}
  const nativeFields = nativeMediaFields(native)
  const ratioDefaults = z
    .object({
      text: z.string().optional(),
      visual_reference: z.string().optional(),
    })
    .safeParse(native['x-default-ratio'])
  const write = (next: MediaParameterDocument) =>
    props.onChange(serializeMediaParameters(next))
  const label = (name: string) =>
    isMediaParameterName(name) ? t(MEDIA_PARAMETERS[name][1]) : name
  const optionLabel = (name: string) => {
    if (!isMediaParameterName(name)) return name
    const text = MEDIA_PARAMETERS[name][1]
    return Object.values(MEDIA_PARAMETERS).filter(([, other]) => other === text)
      .length > 1
      ? `${t(text)} (${name})`
      : t(text)
  }
  const summarize = (field: MediaParameterField | false): string => {
    if (field === false) return t('Disabled')
    if (Object.hasOwn(field, 'const')) return String(field.const)
    if (field.enum?.length) {
      const values = field.enum
      if (
        values.length > 3 &&
        values.every(
          (value, index) =>
            typeof value === 'number' &&
            (index === 0 || value === Number(values[index - 1]) + 1)
        )
      ) {
        return `${String(values[0])}–${String(values.at(-1))}`
      }
      return values
        .map((value) =>
          typeof value === 'boolean' ? t(value ? 'Yes' : 'No') : String(value)
        )
        .join(', ')
    }
    const lower = field.minimum ?? field.minItems,
      upper = field.maximum ?? field.maxItems
    if (lower !== undefined || upper !== undefined) {
      return `${lower ?? '—'}–${upper ?? '—'}`
    }
    if (Object.hasOwn(field, 'default')) {
      return t('Default: {{value}}', { value: String(field.default) })
    }
    return t('Adapter constraint')
  }
  if (props.nativeSchema && !parsedNative) {
    return (
      <p role='alert'>
        {t(
          'Adapter parameters could not be read. Existing settings are preserved; parameter editing is unavailable.'
        )}
      </p>
    )
  }
  if (!document) {
    return (
      <div className='space-y-3' data-generation-invalid='true'>
        <p role='alert'>
          {t(
            'Generation settings could not be read. Existing settings are preserved.'
          )}
        </p>
        <Button
          type='button'
          variant='outline'
          disabled={props.disabled}
          onClick={() => props.onChange('')}
        >
          {t('Use adapter settings')}
        </Button>
      </div>
    )
  }
  const names = [
    ...new Set([
      ...Object.keys(nativeFields),
      ...Object.keys(document.properties ?? {}),
    ]),
  ]
    .filter(isMediaParameterName)
    .sort(
      (a, b) =>
        Object.keys(MEDIA_PARAMETERS).indexOf(a) -
        Object.keys(MEDIA_PARAMETERS).indexOf(b)
    )
  const errors = mediaParameterErrors(document)
  const imported = importMediaCombinations(document, native)
  const ownCombinations = document.oneOf !== undefined
  const combinations = document.oneOf ?? native.oneOf ?? []
  while (combinationIds.current.length < combinations.length) {
    combinationIds.current.push(
      `${combinationPrefix}-${nextCombinationId.current++}`
    )
  }
  const combinationRows = combinations.map((branch, index) => ({
    branch,
    index,
    id: combinationIds.current[index],
  }))
  const selected = Math.min(
    selectedCombination,
    Math.max(0, combinations.length - 1)
  )
  const changeCombination = (index: number, next: MediaParameterObject) => {
    if (!document.oneOf) return
    write({
      ...document,
      oneOf: document.oneOf.map((branch, i) => (i === index ? next : branch)),
    })
  }
  const mayChangeRequired = (document.required ?? native.required ?? []).every(
    isMediaParameterName
  )
  return (
    <div
      className='min-w-0 space-y-5'
      data-generation-invalid={errors.length > 0 || undefined}
    >
      <p className='text-muted-foreground text-sm'>
        {t(
          'Use the form to set choices, defaults, duration, counts and reference limits. Unchanged parameters continue to use the adapter.'
        )}
      </p>
      {ratioDefaults.success && ratioDefaults.data.text && (
        <p className='text-muted-foreground text-sm'>
          {t('Adapter ratio for text generation: {{value}}', {
            value: ratioDefaults.data.text,
          })}
        </p>
      )}
      {ratioDefaults.success && ratioDefaults.data.visual_reference && (
        <p className='text-muted-foreground text-sm'>
          {t('Adapter ratio for reference media: {{value}}', {
            value: ratioDefaults.data.visual_reference,
          })}
        </p>
      )}
      <div className='flex flex-wrap items-center gap-3'>
        <span className='text-sm'>
          {props.value.trim()
            ? t('Custom generation settings')
            : t('Using adapter settings')}
        </span>
        {props.value.trim() && (
          <Button
            type='button'
            variant='outline'
            disabled={props.disabled}
            onClick={() => props.onChange('')}
          >
            {t('Restore all adapter parameters')}
          </Button>
        )}
      </div>
      <div
        className='min-w-0'
        role='group'
        aria-label={t('General generation parameters')}
      >
        {names.map((name) => (
          <MediaParameterFieldEditor
            key={name}
            name={name}
            showName={
              names.filter(
                (other) =>
                  MEDIA_PARAMETERS[other][1] === MEDIA_PARAMETERS[name][1]
              ).length > 1
            }
            field={document.properties?.[name]}
            nativeField={nativeFields[name]}
            required={(document.required ?? native.required ?? []).includes(
              name
            )}
            canChangeRequired={mayChangeRequired}
            disabled={props.disabled}
            onChange={(field) => {
              let next = updateMediaField(document, name, field)
              if (
                field === false &&
                (document.required ?? native.required ?? []).includes(name)
              ) {
                const updated = updateMediaRequired(next, native, name, false)
                if (!updated) return
                next = updated
              }
              write(next)
            }}
            onRequiredChange={(required) => {
              const next = updateMediaRequired(document, native, name, required)
              if (next) write(next)
            }}
          />
        ))}
      </div>
      <div className='max-w-md space-y-2'>
        <label
          className='text-sm font-medium'
          htmlFor={`${combinationPrefix}-add-parameter`}
        >
          {t('Add generation parameter')}
        </label>
        <Combobox
          id={`${combinationPrefix}-add-parameter`}
          aria-label={t('Add generation parameter')}
          value=''
          options={Object.entries(MEDIA_PARAMETERS)
            .filter(([name]) => !names.some((existing) => existing === name))
            .map(([name]) => ({
              value: name,
              label: optionLabel(name),
            }))}
          disabled={props.disabled}
          onValueChange={(name) => {
            if (name && isMediaParameterName(name)) {
              write(updateMediaField(document, name, parameterSeed(name)))
            }
          }}
        />
      </div>
      <section
        className='min-w-0 space-y-3 border-t pt-5'
        aria-label={t('Valid specification combinations')}
      >
        <h4 className='font-medium'>{t('Valid specification combinations')}</h4>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Each request must match an allowed combination. Existing resolution, duration and quality combinations are retained until you customize them.'
          )}
        </p>
        <Combobox
          aria-label={t('Specification combination settings')}
          value={ownCombinations ? 'custom' : 'inherit'}
          options={[
            { value: 'inherit', label: t('Use adapter settings') },
            { value: 'custom', label: t('Customize'), disabled: !imported },
          ]}
          disabled={props.disabled}
          onValueChange={(mode) => {
            if (mode === (ownCombinations ? 'custom' : 'inherit')) return
            if (mode === 'inherit') {
              combinationIds.current = []
              const next = { ...document }
              delete next.oneOf
              write(next)
            } else if (imported) {
              combinationIds.current = []
              write({
                ...imported,
                oneOf: imported.oneOf ?? [{ type: 'object', properties: {} }],
              })
              setSelectedCombination(0)
            }
          }}
        />
        {!imported && (
          <p className='text-muted-foreground text-sm'>
            {t(
              'These adapter combinations include fixed host rules. They are preserved and cannot be replaced by this form.'
            )}
          </p>
        )}
        {combinations.length > 0 ? (
          <Table aria-label={t('Generation specification combinations')}>
            <TableHeader>
              <TableRow>
                <TableHead>{t('Combination')}</TableHead>
                <TableHead>{t('Allowed parameters')}</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {combinationRows.map(({ branch, index, id }) => (
                <TableRow key={id}>
                  <TableCell>{index + 1}</TableCell>
                  <TableCell className='whitespace-normal'>
                    {Object.entries(branch.properties ?? {})
                      .map(
                        ([name, field]) => `${label(name)}: ${summarize(field)}`
                      )
                      .join(' · ') || t('Configure this combination')}
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        ) : (
          <p className='text-muted-foreground text-sm'>
            {t('No additional combination restrictions are configured.')}
          </p>
        )}
        {ownCombinations && (
          <div className='space-y-3'>
            <div className='flex flex-wrap gap-3'>
              <Combobox
                aria-label={t('Edit specification combination')}
                className='w-full sm:w-64'
                value={String(selected)}
                options={combinations.map((_, index) => ({
                  value: String(index),
                  label: t('Combination {{number}}', { number: index + 1 }),
                }))}
                disabled={props.disabled}
                onValueChange={(value) => {
                  if (value !== null) setSelectedCombination(Number(value))
                }}
              />
              <Button
                type='button'
                variant='outline'
                disabled={props.disabled || combinations.length >= 128}
                onClick={() => {
                  write({
                    ...document,
                    oneOf: [
                      ...combinations,
                      { type: 'object', properties: {} },
                    ],
                  })
                  setSelectedCombination(combinations.length)
                }}
              >
                {t('Add combination')}
              </Button>
              <Button
                type='button'
                variant='outline'
                disabled={props.disabled || combinations.length <= 1}
                onClick={() => {
                  combinationIds.current.splice(selected, 1)
                  write({
                    ...document,
                    oneOf: combinations.filter(
                      (_, index) => index !== selected
                    ),
                  })
                  setSelectedCombination(Math.max(0, selected - 1))
                }}
              >
                {t('Remove combination')}
              </Button>
            </div>
            {combinationRows.map(({ branch, index, id }) => (
              <div
                key={id}
                hidden={index !== selected}
                className='min-w-0'
                role='group'
                aria-label={t('Parameters for combination {{number}}', {
                  number: index + 1,
                })}
              >
                {Object.keys(branch.properties ?? {})
                  .filter(isMediaParameterName)
                  .map((name) => (
                    <MediaParameterFieldEditor
                      key={name}
                      name={name}
                      branch
                      field={branch.properties?.[name]}
                      nativeField={
                        document.properties?.[name] ?? nativeFields[name]
                      }
                      required={branch.required?.includes(name) ?? false}
                      canChangeRequired
                      disabled={props.disabled}
                      onChange={(field) => {
                        const next = updateMediaField(branch, name, field)
                        changeCombination(index, next)
                      }}
                      onRequiredChange={(required) => {
                        const next = updateMediaRequired(
                          branch,
                          {},
                          name,
                          required
                        )
                        if (next) changeCombination(index, next)
                      }}
                    />
                  ))}
                <Combobox
                  aria-label={t('Add parameter to combination {{number}}', {
                    number: index + 1,
                  })}
                  value=''
                  options={Object.keys(document.properties ?? {})
                    .filter(
                      (name) =>
                        isMediaParameterName(name) &&
                        !Object.hasOwn(branch.properties ?? {}, name)
                    )
                    .map((name) => ({ value: name, label: label(name) }))}
                  disabled={props.disabled}
                  onValueChange={(name) => {
                    if (name && isMediaParameterName(name)) {
                      changeCombination(
                        index,
                        updateMediaField(branch, name, parameterSeed(name))
                      )
                    }
                  }}
                />
              </div>
            ))}
          </div>
        )}
      </section>
      {Object.keys(nativeFields).some(
        (name) => !isMediaParameterName(name)
      ) && (
        <section className='space-y-2 border-t pt-4'>
          <h4 className='font-medium'>{t('Adapter-managed parameters')}</h4>
          <p className='text-muted-foreground text-sm'>
            {t(
              'These controls and the original upload, task recovery and concurrency rules remain managed by the adapter.'
            )}
          </p>
          <dl className='grid grid-cols-[minmax(0,1fr)_minmax(0,2fr)] gap-2 text-sm'>
            {Object.entries(nativeFields)
              .filter(([name]) => !isMediaParameterName(name))
              .map(([name, field]) => (
                <div className='contents' key={name}>
                  <dt className='break-words'>{label(name)}</dt>
                  <dd className='break-words'>{summarize(field)}</dd>
                </div>
              ))}
          </dl>
        </section>
      )}
      {errors.length > 0 && (
        <div role='alert' className='text-destructive space-y-1 text-sm'>
          {errors.map((error) => (
            <p key={error}>{t(error)}</p>
          ))}
        </div>
      )}
    </div>
  )
}
