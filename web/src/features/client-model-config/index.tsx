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
import { SettingsPageProvider } from '@/features/system-settings/components/settings-page-context'

import { getClientModelCatalog, getClientModelConfig } from './api'
import { ClientModelConfigForm } from './components/config-form'

export function ClientModelConfigPage() {
  const { t } = useTranslation()
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
          <div className='mx-auto max-w-5xl min-w-0'>
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
                models={catalog.data}
              />
            )}
          </div>
        </SectionPageLayout.Content>
      </SectionPageLayout>
    </SettingsPageProvider>
  )
}
