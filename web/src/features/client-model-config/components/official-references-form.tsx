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

import { DatePicker } from '@/components/date-picker'
import { dateInputValue } from '@/components/date-time-utils'
import { Button } from '@/components/ui/button'
import { ComboboxInput } from '@/components/ui/combobox-input'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { SettingsSection } from '@/features/system-settings/components/settings-section'

import {
  newOfficialReference,
  officialReferenceValid,
  referenceSpecificationKey,
} from '../lib/official-references'
import type {
  OfficialPriceReference,
  OfficialReferenceConfiguration,
  OfficialReferenceRow,
} from '../unified-api'

export function OfficialReferencesForm(props: {
  value?: OfficialReferenceConfiguration
  specifications: OfficialReferenceRow[]
  modelName: string
  disabled?: boolean
  onChange: (value: OfficialReferenceConfiguration) => void
}) {
  const { t } = useTranslation()
  const mode = props.value?.mode ?? 'inherit'
  const inherited = props.specifications.filter((row) => row.official_reference)
  const rows = mode === 'override' ? (props.value?.rows ?? []) : inherited
  const invalid =
    mode === 'override' &&
    (!rows.length ||
      new Set(rows.map(referenceSpecificationKey)).size !== rows.length ||
      rows.some((row) => !officialReferenceValid(row)))
  const options = props.specifications.map((row, index) => ({
    value: String(index),
    label: [
      row.direction
        ? t(row.direction === 'input' ? 'Input price' : 'Output price')
        : '',
      ...Object.entries(row.params).map(
        ([key, value]) => `${key}: ${String(value)}`
      ),
      row.unit,
    ]
      .filter(Boolean)
      .join(' · '),
  }))
  const update = (
    index: number,
    field: keyof OfficialPriceReference,
    value: string
  ) => {
    props.onChange({
      mode: 'override',
      rows: rows.map((row, i) => {
        if (i !== index) return row
        const copy = newOfficialReference(row, props.modelName)
        if (!copy.official_reference) return copy
        return {
          ...copy,
          official_reference: { ...copy.official_reference, [field]: value },
        }
      }),
    })
  }
  return (
    <SettingsSection title={t('Official reference prices')}>
      <fieldset
        disabled={props.disabled}
        className='min-w-0 space-y-4'
        data-official-reference-invalid={invalid ? 'true' : undefined}
      >
        <p className='text-muted-foreground text-sm'>
          {t(
            'Official references describe reviewed comparisons only. They do not change user prices, account balances, or existing tasks.'
          )}
        </p>
        <div className='max-w-md space-y-2'>
          <Label htmlFor='official-reference-mode'>
            {t('Reference source')}
          </Label>
          <ComboboxInput
            id='official-reference-mode'
            value={mode}
            options={[
              { value: 'inherit', label: t('Inherit reviewed references') },
              { value: 'override', label: t('Configure official references') },
              { value: 'disabled', label: t('Disable price comparisons') },
            ]}
            onValueChange={(next) => {
              if (next === 'override') {
                props.onChange({
                  mode: next,
                  rows: inherited.map((row) =>
                    newOfficialReference(row, props.modelName)
                  ),
                })
              } else if (next === 'inherit' || next === 'disabled') {
                props.onChange({ mode: next })
              }
            }}
          />
        </div>
        {mode !== 'disabled' && (
          <div className='min-w-0 overflow-x-auto rounded-md border'>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('Specification and direction')}</TableHead>
                  <TableHead>{t('Official price in CNY')}</TableHead>
                  <TableHead>{t('Official model')}</TableHead>
                  <TableHead>{t('Official source and checked date')}</TableHead>
                  <TableHead>{t('Conditions and conversion')}</TableHead>
                  {mode === 'override' && <TableHead>{t('Actions')}</TableHead>}
                </TableRow>
              </TableHeader>
              <TableBody>
                {rows.map((row, index) => {
                  const ref = row.official_reference
                  const specIndex = props.specifications.findIndex(
                    (spec) =>
                      referenceSpecificationKey(spec) ===
                      referenceSpecificationKey(row)
                  )
                  let selected: Date | undefined
                  if (ref?.checked_at) {
                    const candidate = new Date(`${ref.checked_at}T00:00:00`)
                    if (Number.isFinite(candidate.getTime())) {
                      selected = candidate
                    }
                  }
                  return (
                    <TableRow key={referenceSpecificationKey(row)}>
                      <TableCell className='min-w-56 align-top'>
                        <ComboboxInput
                          aria-label={t('Specification and direction')}
                          disabled={mode !== 'override'}
                          options={options}
                          value={specIndex < 0 ? '' : String(specIndex)}
                          onValueChange={(value) => {
                            const spec = props.specifications[Number(value)]
                            if (spec) {
                              props.onChange({
                                mode: 'override',
                                rows: rows.map((item, i) =>
                                  i === index
                                    ? newOfficialReference(
                                        spec,
                                        props.modelName
                                      )
                                    : item
                                ),
                              })
                            }
                          }}
                        />
                        <p className='text-muted-foreground mt-2 text-xs'>
                          {row.unit}
                        </p>
                      </TableCell>
                      <TableCell className='min-w-40 align-top'>
                        <Input
                          aria-label={t('Official price in CNY')}
                          inputMode='decimal'
                          value={ref?.official_cny ?? ''}
                          disabled={mode !== 'override'}
                          aria-required
                          aria-invalid={
                            mode === 'override' && !officialReferenceValid(row)
                          }
                          onChange={(event) =>
                            update(index, 'official_cny', event.target.value)
                          }
                        />
                      </TableCell>
                      <TableCell className='min-w-48 align-top'>
                        <Input
                          aria-label={t('Official model')}
                          value={ref?.model ?? ''}
                          disabled={mode !== 'override'}
                          maxLength={191}
                          onChange={(event) =>
                            update(index, 'model', event.target.value)
                          }
                        />
                      </TableCell>
                      <TableCell className='min-w-72 space-y-2 align-top'>
                        <Input
                          aria-label={t('Official source URL')}
                          type='url'
                          value={ref?.source_url ?? ''}
                          disabled={mode !== 'override'}
                          placeholder='https://'
                          maxLength={2048}
                          onChange={(event) =>
                            update(index, 'source_url', event.target.value)
                          }
                        />
                        <fieldset disabled={mode !== 'override'}>
                          <DatePicker
                            aria-label={t('Official checked date')}
                            selected={selected}
                            onSelect={(date) =>
                              update(index, 'checked_at', dateInputValue(date))
                            }
                          />
                        </fieldset>
                      </TableCell>
                      <TableCell className='min-w-64 space-y-2 align-top'>
                        <Input
                          aria-label={t('Conditions')}
                          value={ref?.conditions ?? ''}
                          disabled={mode !== 'override'}
                          maxLength={4096}
                          onChange={(event) =>
                            update(index, 'conditions', event.target.value)
                          }
                        />
                        <ComboboxInput
                          aria-label={t('Original reference currency')}
                          disabled={mode !== 'override'}
                          value={ref?.source_currency ?? 'CNY'}
                          options={['CNY', 'USD'].map((value) => ({
                            value,
                            label: value,
                          }))}
                          onValueChange={(value) =>
                            update(index, 'source_currency', value)
                          }
                        />
                        {ref?.source_currency === 'USD' && (
                          <Input
                            aria-label={t('Recorded CNY per USD')}
                            inputMode='decimal'
                            disabled={mode !== 'override'}
                            value={ref.usd_cny ?? ''}
                            onChange={(event) =>
                              update(index, 'usd_cny', event.target.value)
                            }
                          />
                        )}
                        {ref?.official_total_cny !== undefined && (
                          <Input
                            aria-label={t('Official total price in CNY')}
                            inputMode='decimal'
                            disabled={mode !== 'override'}
                            value={ref.official_total_cny}
                            onChange={(event) =>
                              update(
                                index,
                                'official_total_cny',
                                event.target.value
                              )
                            }
                          />
                        )}
                      </TableCell>
                      {mode === 'override' && (
                        <TableCell className='align-top'>
                          <Button
                            type='button'
                            variant='ghost'
                            aria-label={t('Remove official reference')}
                            onClick={() =>
                              props.onChange({
                                mode,
                                rows: rows.filter((_, i) => i !== index),
                              })
                            }
                          >
                            {t('Remove')}
                          </Button>
                        </TableCell>
                      )}
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          </div>
        )}
        {mode === 'override' && (
          <Button
            type='button'
            variant='outline'
            disabled={
              props.disabled ||
              !props.specifications.length ||
              rows.length >= 256
            }
            onClick={() => {
              const spec = props.specifications.find(
                (candidate) =>
                  !rows.some(
                    (row) =>
                      referenceSpecificationKey(row) ===
                      referenceSpecificationKey(candidate)
                  )
              )
              if (spec) {
                props.onChange({
                  mode,
                  rows: [...rows, newOfficialReference(spec, props.modelName)],
                })
              }
            }}
          >
            {t('Add official reference')}
          </Button>
        )}
        {mode !== 'disabled' && rows.length === 0 && (
          <p className='text-muted-foreground text-sm'>
            {t(
              'No reviewed reference is configured. User pricing stays unchanged.'
            )}
          </p>
        )}
        {invalid && (
          <p role='alert' className='text-destructive text-sm'>
            {t(
              'Complete the official price, public HTTPS source, and checked date before saving.'
            )}
          </p>
        )}
      </fieldset>
    </SettingsSection>
  )
}
