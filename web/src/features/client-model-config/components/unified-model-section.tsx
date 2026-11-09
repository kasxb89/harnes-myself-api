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
import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { ComboboxInput } from '@/components/ui/combobox-input'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { invalidateModelPricing } from '@/features/model-pricing/api'
import { SettingsSection } from '@/features/system-settings/components/settings-section'
import { formatTimestamp } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'

import {
  getUnifiedModelConfiguration,
  publishUnifiedModels,
  saveUnifiedModelDraft,
  type UnifiedModelDefinition,
} from '../unified-api'
import { UnifiedModelForm } from './unified-model-form'

export function UnifiedModelSection(props: {
  initialModel?: string
  active: boolean
  onDirtyChange: (dirty: boolean) => void
}) {
  const { t } = useTranslation()
  const cache = useQueryClient()
  const [selected, setSelected] = useState(props.initialModel ?? '')
  const [newID, setNewID] = useState('')
  const [dirty, setDirty] = useState(false)
  const [pendingModel, setPendingModel] = useState<string | null>(null)
  const onParentDirty = props.onDirtyChange
  const onDirtyChange = useCallback(
    (value: boolean) => {
      setDirty(value)
      onParentDirty(value)
    },
    [onParentDirty]
  )
  const selectModel = (value: string) => {
    if (dirty) {
      setPendingModel(value)
    } else {
      setSelected(value)
    }
  }
  const [confirm, setConfirm] = useState<'publish' | string | null>(null)
  const inventory = useQuery({
    queryKey: ['unified-models'],
    enabled: props.active,
    queryFn: () => getUnifiedModelConfiguration(),
  })
  const query = useQuery({
    queryKey: ['unified-models', selected],
    queryFn: () => getUnifiedModelConfiguration(selected),
    enabled: props.active && Boolean(selected),
  })
  const entry = query.data?.entries[0]
  const publication = useMutation({
    mutationFn: () =>
      publishUnifiedModels(
        query.data?.revision ?? '',
        confirm === 'publish' ? [selected] : [],
        confirm === 'publish' ? undefined : (confirm ?? undefined)
      ),
    onSuccess: async () => {
      setConfirm(null)
      onDirtyChange(false)
      await Promise.all([
        cache.invalidateQueries({ queryKey: ['unified-models'] }),
        cache.invalidateQueries({ queryKey: ['client-model-config'] }),
        cache.invalidateQueries({ queryKey: ['configured-model-pricebook'] }),
        cache.invalidateQueries({ queryKey: ['model-profits'] }),
        invalidateModelPricing(cache),
      ])
      toast.success(t('Model configuration published'))
    },
    onError: handleServerError,
  })
  const save = useMutation({
    mutationFn: (definition: UnifiedModelDefinition) =>
      saveUnifiedModelDraft(
        query.data?.revision ?? '',
        entry?.base_version ?? '',
        definition
      ),
    onSuccess: async () => {
      onDirtyChange(false)
      await cache.invalidateQueries({ queryKey: ['unified-models'] })
      toast.success(t('Model draft validated and saved'))
    },
  })
  if (inventory.isPending) return <LoadingState />
  if (!inventory.data) {
    return (
      <ErrorState
        title={t('Failed to load unified model configuration')}
        onRetry={() => void inventory.refetch()}
      />
    )
  }
  return (
    <div className='space-y-6'>
      <p className='text-muted-foreground max-w-prose text-sm'>
        {t(
          'Manage model availability, user prices, procurement costs and generation limits together. Publish only after reviewing the saved draft.'
        )}
      </p>
      <div className='space-y-2'>
        <Label htmlFor='unified-model-picker'>
          {t('All configured models')}
        </Label>
        <ComboboxInput
          id='unified-model-picker'
          value={selected}
          onValueChange={selectModel}
          options={inventory.data.entries.map((item) => ({
            value: item.model_name,
            label: item.definition.display_name,
            description: `${item.model_name} · ${item.definition.enabled ? t('Enabled') : t('Disabled')}${item.draft ? ` · ${t('Draft')}` : ''}`,
          }))}
        />
      </div>
      <SettingsSection
        title={t('Add a model on an existing channel')}
        className='border-b pb-6'
      >
        <div className='flex flex-wrap items-end gap-3'>
          <div className='min-w-0 flex-1 space-y-2'>
            <Label htmlFor='unified-new-id'>{t('Public model ID')}</Label>
            <Input
              id='unified-new-id'
              value={newID}
              onChange={(event) => setNewID(event.target.value)}
            />
          </div>
          <Button
            variant='outline'
            disabled={!newID.trim()}
            onClick={() => {
              selectModel(newID.trim())
              setNewID('')
            }}
          >
            {t('Configure model')}
          </Button>
        </div>
      </SettingsSection>
      {selected && query.isPending && <LoadingState />}
      {selected && query.isError && (
        <ErrorState
          title={t('Failed to load unified model configuration')}
          onRetry={() => void query.refetch()}
        />
      )}
      {entry && query.data && (
        <>
          <div className='flex flex-wrap items-center justify-between gap-3 py-2'>
            <div className='min-w-0'>
              <h2 className='font-semibold break-all'>{entry.model_name}</h2>
              <p className='text-muted-foreground text-sm'>
                {entry.published
                  ? t('Published configuration')
                  : t('Existing configuration')}
                {entry.draft ? ` · ${t('Draft saved')}` : ''}
              </p>
            </div>
            <Button
              variant='outline'
              disabled={
                dirty || !entry.draft || publication.isPending || save.isPending
              }
              onClick={() => setConfirm('publish')}
            >
              {t('Publish saved draft')}
            </Button>
          </div>
          <UnifiedModelForm
            key={`${entry.model_name}:${query.data.revision}`}
            entry={entry}
            channels={query.data.channels}
            pending={publication.isPending || save.isPending}
            onSave={async (definition) => {
              await save.mutateAsync(definition)
            }}
            onDirtyChange={onDirtyChange}
          />
          <SettingsSection
            title={t('Publication history and rollback')}
            className='border-t pt-6'
          >
            <p className='text-muted-foreground'>
              {t(
                'Rollback publishes a new configuration for future requests. Existing tasks and charges are not replayed.'
              )}
            </p>
            <ul className='divide-y'>
              {[...query.data.history]
                .reverse()
                .filter((item) => item.model_name === entry.model_name)
                .map((item) => (
                  <li
                    key={item.version}
                    className='flex min-w-0 items-center justify-between gap-3 py-3'
                  >
                    <span
                      className='min-w-0 space-y-1 text-sm'
                      title={item.version}
                    >
                      <span className='block'>
                        {formatTimestamp(item.published_at)}
                      </span>
                      <code className='text-muted-foreground block truncate text-xs'>
                        {item.version.slice(0, 12)}
                      </code>
                    </span>
                    <Button
                      variant='ghost'
                      disabled={
                        publication.isPending ||
                        save.isPending ||
                        dirty ||
                        item.version === entry.published?.version
                      }
                      onClick={() => setConfirm(item.version)}
                    >
                      {t('Restore this configuration')}
                    </Button>
                  </li>
                ))}
            </ul>
          </SettingsSection>
        </>
      )}
      <ConfirmDialog
        open={pendingModel !== null}
        onOpenChange={(open) => {
          if (!open) setPendingModel(null)
        }}
        title={t('Discard unsaved changes?')}
        desc={t(
          'The unsaved edits will be discarded when switching models. Saved drafts remain available.'
        )}
        confirmText={t('Discard and switch')}
        handleConfirm={() => {
          if (pendingModel !== null) {
            setSelected(pendingModel)
            setPendingModel(null)
            onDirtyChange(false)
          }
        }}
      />
      <ConfirmDialog
        open={confirm !== null}
        onOpenChange={(open) => {
          if (!open) setConfirm(null)
        }}
        title={t('Publish model configuration?')}
        desc={t(
          'New requests will use the reviewed configuration. Accepted tasks retain their original request and billing snapshot.'
        )}
        confirmText={t('Publish')}
        isLoading={publication.isPending}
        handleConfirm={() => publication.mutate(undefined)}
      />
    </div>
  )
}
