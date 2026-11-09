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

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { FormNavigationGuard } from '@/features/system-settings/components/form-navigation-guard'
import { SettingsPageProvider } from '@/features/system-settings/components/settings-page-context'
import { canAccessAdminRoute, hasPermission } from '@/lib/admin-permissions'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { getClientModelCatalog, getClientModelConfig } from './api'
import { ClientModelConfigForm } from './components/config-form'
import { ClientDefaultsSummary } from './components/defaults-summary'
import { ClientMediaDefaultsSection } from './components/media-defaults-form'
import { ClientModelPresentationSection } from './components/presentation-form'
import { UnifiedModelSection } from './components/unified-model-section'

export function ClientModelConfigPage(
  props: { initialModel?: string; initialTab?: string } = {}
) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  if (!canAccessAdminRoute(user, '/client-model-config')) {
    return <ErrorState title={t('Access denied')} />
  }
  return (
    <ClientModelConfigEditor
      key={`${user?.id}:${props.initialModel ?? ''}:${props.initialTab ?? ''}`}
      initialModel={props.initialModel}
      initialTab={props.initialTab}
      readOnly={!hasPermission(user, 'client_model_config', 'write')}
      canEditCapacities={(user?.role ?? 0) >= ROLE.SUPER_ADMIN}
    />
  )
}

function ClientModelConfigEditor(props: {
  initialModel?: string
  initialTab?: string
  readOnly: boolean
  canEditCapacities: boolean
}) {
  const { t } = useTranslation()
  const [tab, setTab] = useState(
    props.initialModel ? 'unified' : (props.initialTab ?? 'preferences')
  )
  const [configDirty, setConfigDirty] = useState(false)
  const [mediaDirty, setMediaDirty] = useState(false)
  const [presentationDirty, setPresentationDirty] = useState(false)
  const [unifiedDirty, setUnifiedDirty] = useState(false)
  const [actions, setActions] = useState<HTMLDivElement | null>(null)
  const [status, setStatus] = useState<HTMLSpanElement | null>(null)
  const config = useQuery({
    queryKey: ['client-model-config'],
    queryFn: getClientModelConfig,
  })
  const catalog = useQuery({
    queryKey: ['client-model-config', 'models'],
    queryFn: getClientModelCatalog,
  })
  const pending = config.isPending || catalog.isPending
  const error =
    (!config.data && config.isError) || (!catalog.data && catalog.isError)

  return (
    <SettingsPageProvider
      actionsContainer={actions}
      titleStatusContainer={status}
      suppressSectionHeader={false}
    >
      <SectionPageLayout stackActionsOnMobile>
        <SectionPageLayout.Title>
          <span className='inline-flex max-w-full min-w-0 flex-wrap items-center gap-2'>
            <span className='truncate'>{t('Client model configuration')}</span>
            <span ref={setStatus} />
          </span>
        </SectionPageLayout.Title>
        <SectionPageLayout.Actions>
          <div
            ref={setActions}
            role='group'
            aria-label={t('Client model configuration')}
            className='flex max-w-[calc(100vw-2rem)] flex-wrap gap-2'
          />
        </SectionPageLayout.Actions>
        <SectionPageLayout.Content>
          <FormNavigationGuard
            when={
              configDirty || presentationDirty || mediaDirty || unifiedDirty
            }
          />
          <Tabs
            value={tab}
            onValueChange={(value) => setTab(String(value))}
            className='mx-auto max-w-5xl min-w-0 gap-4'
          >
            {config.data && tab !== 'unified' && (
              <ClientDefaultsSummary config={config.data} />
            )}
            <TabsList
              variant='line'
              className='grid h-auto w-full grid-cols-2 justify-start gap-x-4 gap-y-2 border-b pb-2 group-data-horizontal/tabs:h-auto sm:flex sm:flex-wrap'
              aria-label={t('Client model settings')}
            >
              {props.canEditCapacities && (
                <TabsTrigger
                  value='unified'
                  className='h-auto whitespace-normal'
                >
                  {t('Models, prices and parameters')}
                </TabsTrigger>
              )}
              <TabsTrigger
                value='preferences'
                className='h-auto whitespace-normal'
              >
                {t('Defaults and order')}
              </TabsTrigger>
              <TabsTrigger value='media' className='h-auto whitespace-normal'>
                {t('Image and video defaults')}
              </TabsTrigger>
              <TabsTrigger
                value='presentation'
                className='h-auto whitespace-normal'
              >
                {t('Descriptions and badges')}
              </TabsTrigger>
            </TabsList>
            {props.canEditCapacities && (
              <TabsContent value='unified' keepMounted>
                <UnifiedModelSection
                  initialModel={props.initialModel}
                  active={tab === 'unified'}
                  onDirtyChange={setUnifiedDirty}
                />
              </TabsContent>
            )}
            <TabsContent value='preferences' keepMounted>
              {pending && (
                <LoadingState
                  message={t('Loading client model configuration...')}
                />
              )}
              {!pending && error && (
                <ErrorState
                  title={t('Failed to load client model configuration')}
                  onRetry={() => {
                    void config.refetch()
                    void catalog.refetch()
                  }}
                />
              )}
              {!pending && !error && config.data && catalog.data && (
                <ClientModelConfigForm
                  config={config.data}
                  readOnly={props.readOnly}
                  models={catalog.data}
                  active={tab === 'preferences'}
                  onDirtyChange={setConfigDirty}
                />
              )}
            </TabsContent>
            <TabsContent value='media' keepMounted>
              {catalog.data && (
                <ClientMediaDefaultsSection
                  models={catalog.data}
                  readOnly={props.readOnly}
                  active={tab === 'media'}
                  onDirtyChange={setMediaDirty}
                />
              )}
              {!catalog.data && catalog.isPending && <LoadingState />}
              {!catalog.data && !catalog.isPending && (
                <ErrorState
                  title={t('Failed to load client model catalog')}
                  onRetry={() => void catalog.refetch()}
                />
              )}
            </TabsContent>
            <TabsContent value='presentation' keepMounted>
              <ClientModelPresentationSection
                readOnly={props.readOnly}
                onDirtyChange={setPresentationDirty}
                models={catalog.data ?? []}
                catalogUnavailable={catalog.isError && !catalog.data}
                onRetryCatalog={() => {
                  void catalog.refetch()
                }}
              />
            </TabsContent>
          </Tabs>
        </SectionPageLayout.Content>
      </SectionPageLayout>
    </SettingsPageProvider>
  )
}
