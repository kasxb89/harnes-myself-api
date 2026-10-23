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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Field, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { FormDirtyIndicator } from '@/features/system-settings/components/form-dirty-indicator'
import { FormNavigationGuard } from '@/features/system-settings/components/form-navigation-guard'
import { SettingsCard } from '@/features/system-settings/components/settings-card'
import { handleServerError } from '@/lib/handle-server-error'

import { getClientModelPresentation, saveClientModelPresentation } from '../api'
import {
  clientModelPresentationSchema,
  sameClientModelPresentation,
} from '../lib/presentation'
import type {
  ClientModelOption,
  ClientModelPresentation,
  ClientModelPresentationEntry,
} from '../types'
import { PresentationDialog } from './presentation-dialog'
import { PresentationTable } from './presentation-table'

type PresentationSectionProps = {
  onDirtyChange?: (dirty: boolean) => void
  models: ClientModelOption[]
  catalogUnavailable: boolean
  onRetryCatalog: () => void
}

export function ClientModelPresentationSection(
  props: PresentationSectionProps
) {
  const { t } = useTranslation()
  const presentation = useQuery({
    queryKey: ['client-model-presentation'],
    queryFn: getClientModelPresentation,
  })
  return (
    <SettingsCard
      title={t('Model descriptions and badges')}
      description={t(
        'Edit the plain-text description and single badge for each exact model ID. These settings are saved separately from model defaults and order.'
      )}
    >
      {presentation.isPending && (
        <LoadingState message={t('Loading model descriptions and badges...')} />
      )}
      {!presentation.data && presentation.isError && (
        <ErrorState
          title={t('Failed to load client model presentation')}
          onRetry={() => {
            void presentation.refetch()
          }}
        />
      )}
      {presentation.data && (
        <ClientModelPresentationForm
          {...props}
          presentation={presentation.data}
        />
      )}
    </SettingsCard>
  )
}

type PresentationFormProps = PresentationSectionProps & {
  presentation: ClientModelPresentation
}

function ClientModelPresentationForm(props: PresentationFormProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [search, setSearch] = useState('')
  const [editing, setEditing] = useState<string | null>(null)
  const [draft, setDraft] = useState(props.presentation)
  const [baseline, setBaseline] = useState(props.presentation)
  const save = useMutation({
    mutationFn: saveClientModelPresentation,
    onSuccess: (data) => {
      setDraft(data)
      setBaseline(data)
      queryClient.setQueryData(['client-model-presentation'], data)
      void queryClient.invalidateQueries({ queryKey: ['status'] })
      toast.success(t('Model descriptions and badges saved'))
    },
    onError: (error) => handleServerError(error),
  })
  const dirty = !sameClientModelPresentation(draft, baseline)
  const onDirtyChange = props.onDirtyChange
  useEffect(() => {
    onDirtyChange?.(dirty)
  }, [dirty, onDirtyChange])
  useEffect(() => {
    if (!dirty && !save.isPending) {
      setDraft(props.presentation)
      setBaseline(props.presentation)
    }
  }, [props.presentation, dirty, save.isPending])
  const entries = draft.models
  const valid = clientModelPresentationSchema.safeParse({
    schema_version: 1,
    models: entries,
  }).success
  const onSave = async () => {
    if (!valid || !dirty || save.isPending) return
    try {
      await save.mutateAsync(draft)
    } catch {
      /* Preserve the draft for retry. */
    }
  }
  const onApply = (entry: ClientModelPresentationEntry) => {
    if (editing === null) return
    const next = { ...entries, [editing]: entry }
    if (Object.keys(entry).length === 0) delete next[editing]
    setDraft({ schema_version: 1, models: next })
    setEditing(null)
  }
  return (
    <div className='flex min-w-0 flex-col gap-4'>
      <FormDirtyIndicator
        isDirty={dirty}
        message='Unsaved description and badge changes'
      />
      <FormNavigationGuard when={!props.onDirtyChange && dirty} />
      {props.catalogUnavailable && (
        <Alert>
          <AlertDescription>
            {t(
              'The model catalog is unavailable. Saved descriptions and badges remain editable.'
            )}
            <Button
              type='button'
              variant='outline'
              size='sm'
              onClick={props.onRetryCatalog}
            >
              {t('Retry model catalog')}
            </Button>
          </AlertDescription>
        </Alert>
      )}
      {save.isError && (
        <Alert variant='destructive'>
          <AlertDescription>
            {t(
              'Saving failed. Your draft is preserved. Retry saving when ready.'
            )}
          </AlertDescription>
        </Alert>
      )}
      {!valid && (
        <Alert variant='destructive'>
          <AlertDescription>
            {t(
              'You can configure up to 256 model presentations. Reset a model to client defaults to free a slot.'
            )}
          </AlertDescription>
        </Alert>
      )}
      <div className='flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between'>
        <Field className='sm:max-w-sm'>
          <FieldLabel htmlFor='client-model-presentation-search'>
            {t('Search model IDs')}
          </FieldLabel>
          <Input
            id='client-model-presentation-search'
            type='search'
            value={search}
            onChange={(event) => setSearch(event.target.value)}
          />
        </Field>
        <div
          className='flex flex-wrap gap-2'
          role='group'
          aria-label={t('Description and badge actions')}
        >
          <Button
            type='button'
            variant='outline'
            disabled={!dirty || save.isPending}
            onClick={() => {
              setDraft(props.presentation)
              setBaseline(props.presentation)
            }}
          >
            {t('Discard description and badge changes')}
          </Button>
          <Button
            type='button'
            disabled={!dirty || !valid || save.isPending}
            onClick={() => {
              void onSave()
            }}
          >
            {save.isPending
              ? t('Saving...')
              : t('Save descriptions and badges')}
          </Button>
        </div>
      </div>
      <PresentationTable
        models={props.models}
        entries={entries}
        search={search}
        disabled={save.isPending}
        onEdit={setEditing}
      />
      {editing !== null && (
        <PresentationDialog
          key={editing}
          modelId={editing}
          entry={entries[editing] ?? {}}
          onApply={onApply}
          onClose={() => setEditing(null)}
        />
      )}
    </div>
  )
}
