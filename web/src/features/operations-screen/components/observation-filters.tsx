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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { ComboboxInput } from '@/components/ui/combobox-input'
import { Field, FieldLabel } from '@/components/ui/field'
import { CompactDateTimeRangePicker } from '@/features/usage-logs/components/compact-date-time-range-picker'

import type { OperationsSnapshot } from '../api'
import {
  supportsOperationsModelFilter,
  validOperationsFilters,
  validOperationsModelName,
  type OperationsTab,
  type OperationsWindow,
} from '../policy'

export function OperationsObservationFilters(props: {
  tab: OperationsTab
  snapshot?: OperationsSnapshot
  window?: OperationsWindow
  customWindow?: OperationsWindow
  modelName: string
  loading: boolean
  onApply: (filters: {
    modelName: string
    customWindow?: OperationsWindow
  }) => void
}) {
  const { t } = useTranslation()
  const [modelDraft, setModelDraft] = useState(props.modelName)
  const [timeDraft, setTimeDraft] = useState<{ start?: Date; end?: Date }>()
  const modelEnabled = supportsOperationsModelFilter(props.tab)
  const timeEnabled = props.tab !== 'concurrency'
  const currentWindow = props.customWindow ?? props.window
  let start = currentWindow
    ? new Date(currentWindow.start_timestamp * 1000)
    : undefined
  let end = currentWindow
    ? new Date(currentWindow.end_timestamp * 1000)
    : undefined
  if (timeDraft) {
    start = timeDraft.start
    end = timeDraft.end
  }
  const candidate =
    start && end
      ? {
          start_timestamp: Math.floor(start.getTime() / 1000),
          end_timestamp: Math.min(
            Math.floor(end.getTime() / 1000),
            Math.floor(Date.now() / 1000)
          ),
        }
      : undefined
  const selectedModel = modelEnabled ? modelDraft.trim() : ''
  const valid =
    !timeEnabled ||
    Boolean(
      candidate && validOperationsFilters(props.tab, candidate, selectedModel)
    )
  const maxDays =
    props.tab === 'image' || props.tab === 'video' || props.tab === 'activity'
      ? 31
      : 366
  const snapshot = props.snapshot
  let modelOptions: string[] = []
  if (snapshot?.tab === 'profit') {
    modelOptions =
      snapshot.report.model_options ??
      snapshot.report.rows.map((row) => row.model_name)
  } else if (snapshot?.tab === 'image' || snapshot?.tab === 'video') {
    modelOptions = snapshot.report.models.map((row) => row.model)
  } else if (snapshot?.tab === 'usage' || snapshot?.tab === 'errors') {
    modelOptions = snapshot.logs.items.map((row) => row.model_name)
  }
  const models = [...new Set(modelOptions.filter(Boolean))].sort()
  return (
    <form
      className='ops-filter-console space-y-2'
      onSubmit={(event) => {
        event.preventDefault()
        if (!valid || !timeEnabled || props.loading) return
        props.onApply({
          modelName: selectedModel,
          customWindow: timeDraft ? candidate : props.customWindow,
        })
        setModelDraft(selectedModel)
        setTimeDraft(undefined)
      }}
    >
      <div className='grid min-w-0 items-end gap-3 md:grid-cols-[minmax(0,1.4fr)_minmax(0,1fr)_auto]'>
        <Field>
          <FieldLabel>{t('Query time window')}</FieldLabel>
          {timeEnabled ? (
            <CompactDateTimeRangePicker
              start={start}
              end={end}
              onChange={setTimeDraft}
              className='ops-custom-time'
            />
          ) : (
            <p className='text-muted-foreground min-h-8 content-center text-xs'>
              {t('Current observation')}
            </p>
          )}
        </Field>
        <Field>
          <FieldLabel htmlFor='ops-screen-model'>{t('Model')}</FieldLabel>
          <ComboboxInput
            id='ops-screen-model'
            allowCustomValue
            disabled={!modelEnabled}
            value={modelEnabled ? modelDraft : ''}
            onValueChange={setModelDraft}
            options={[
              { value: '', label: t('All models') },
              ...models.map((model) => ({ value: model, label: model })),
            ]}
            placeholder={t('All models')}
            popupClassName='ops-screen-popover'
            aria-invalid={
              modelEnabled && !validOperationsModelName(selectedModel)
            }
          />
        </Field>
        <Button
          type='submit'
          variant='outline'
          disabled={!timeEnabled || !valid || props.loading}
        >
          {t('Apply filters')}
        </Button>
      </div>
      {!timeEnabled && (
        <p className='text-muted-foreground text-xs'>
          {t(
            'Concurrency is a current global snapshot. Model and historical time filters do not apply.'
          )}
        </p>
      )}
      {props.tab === 'activity' && (
        <p className='text-muted-foreground text-xs'>
          {t(
            'Model filtering is unavailable for user activity. Current presence stays global; the selected time filters historical activity only.'
          )}
        </p>
      )}
      {timeEnabled && (
        <p className='text-muted-foreground text-xs'>
          {t(
            'Typed timestamps use your browser timezone. Future end times are capped at now. Confirm the time range, then apply filters.'
          )}
        </p>
      )}
      {timeEnabled && !valid && (
        <p role='alert' className='text-sm'>
          {t(
            'Choose a valid past time range of up to {{days}} days and a model name of at most 255 bytes.',
            { days: maxDays }
          )}
        </p>
      )}
    </form>
  )
}
