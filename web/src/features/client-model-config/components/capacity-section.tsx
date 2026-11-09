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
import { useQuery } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Combobox } from '@/components/ui/combobox'
import { Label } from '@/components/ui/label'
import { SettingsSection } from '@/features/system-settings/components/settings-section'

import { getClientModelCapacities } from '../api'
import type { ClientModelCapacityCatalog, ClientModelOption } from '../types'
import { ClientModelCapacityForm } from './capacity-form'

type CapacitySectionProps = {
  active: boolean
  models: ClientModelOption[]
  onDirtyChange: (dirty: boolean) => void
}

export function ClientModelCapacitySection(props: CapacitySectionProps) {
  const { t } = useTranslation()
  const [modelChoice, setModelChoice] = useState('')
  const [channelChoice, setChannelChoice] = useState('')
  const [dirty, setDirty] = useState(false)
  const [draftCatalog, setDraftCatalog] = useState<ClientModelCapacityCatalog>()
  const catalog = useQuery({
    queryKey: ['client-model-capacities'],
    queryFn: getClientModelCapacities,
    enabled: props.active,
  })
  const displayedCatalog = dirty && draftCatalog ? draftCatalog : catalog.data
  const routes = displayedCatalog?.routes ?? []
  const modelIds = [...new Set(routes.map((row) => row.model))].sort()
  const selectedModel = modelIds.includes(modelChoice)
    ? modelChoice
    : (modelIds[0] ?? '')
  const channelIds = [
    ...new Set(
      routes
        .filter((row) => row.model === selectedModel)
        .map((row) => row.channel_id)
    ),
  ]
  const selectedChannel = channelIds.includes(Number(channelChoice))
    ? Number(channelChoice)
    : channelIds[0]
  const routeUnavailable =
    dirty &&
    routes.some(
      (row) =>
        row.model === selectedModel &&
        row.channel_id === selectedChannel &&
        !catalog.data?.routes.some(
          (current) =>
            current.model === row.model &&
            current.channel_id === row.channel_id &&
            current.group === row.group &&
            current.route_model === row.route_model
        )
    )
  return (
    <SettingsSection
      title={t('Context and output limits')}
      description={t(
        'Configure chat model limits here. Signed-in clients refresh model settings automatically, usually within one minute.'
      )}
    >
      {catalog.isPending && (
        <LoadingState message={t('Loading model capacities...')} />
      )}
      {!catalog.data && catalog.isError && (
        <ErrorState
          title={t('Failed to load model capacities')}
          onRetry={() => void catalog.refetch()}
        />
      )}
      {displayedCatalog && modelIds.length === 0 && (
        <EmptyState title={t('No available chat model routes')} />
      )}
      {displayedCatalog && catalog.data && modelIds.length > 0 && (
        <div className='min-w-0 space-y-6'>
          <div className='grid min-w-0 gap-4 sm:grid-cols-2'>
            <div className='min-w-0 space-y-2'>
              <Label htmlFor='capacity-model'>{t('Model')}</Label>
              <Combobox
                id='capacity-model'
                aria-label={t('Model')}
                className='w-full'
                options={modelIds.map((id) => ({
                  value: id,
                  label: id,
                  description: props.models.find((model) => model.id === id)
                    ?.display_name,
                }))}
                value={selectedModel}
                disabled={dirty}
                onValueChange={(value) => {
                  setModelChoice(value ?? '')
                  setChannelChoice('')
                }}
              />
            </div>
            <div className='min-w-0 space-y-2'>
              <Label htmlFor='capacity-channel'>{t('Channel')}</Label>
              <Combobox
                id='capacity-channel'
                aria-label={t('Channel')}
                className='w-full'
                options={channelIds.map((id) => ({
                  value: String(id),
                  label: `#${id} ${routes.find((row) => row.channel_id === id)?.channel_name ?? ''}`,
                }))}
                value={
                  selectedChannel === undefined ? '' : String(selectedChannel)
                }
                disabled={dirty}
                onValueChange={(value) => setChannelChoice(value ?? '')}
              />
            </div>
          </div>
          {selectedChannel !== undefined && (
            <ClientModelCapacityForm
              key={`${selectedModel}:${selectedChannel}`}
              active={props.active}
              catalog={catalog.data}
              routeUnavailable={routeUnavailable}
              model={selectedModel}
              channelId={selectedChannel}
              onDirtyChange={(value) => {
                if (value && !dirty) setDraftCatalog(catalog.data)
                if (!value) setDraftCatalog(undefined)
                setDirty(value)
                props.onDirtyChange(value)
              }}
              onReload={() => void catalog.refetch()}
            />
          )}
        </div>
      )}
    </SettingsSection>
  )
}
