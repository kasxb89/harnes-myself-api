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
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { SettingsCard } from '@/features/system-settings/components/settings-card'

import { getClientMediaDefaults } from '../api'
import type { ClientModelConfig } from '../types'

export function ClientDefaultsSummary(props: { config: ClientModelConfig }) {
  const { t } = useTranslation()
  const media = useQuery({
    queryKey: ['client-media-defaults'],
    queryFn: getClientMediaDefaults,
  })
  return (
    <section aria-label={t('Saved platform defaults')}>
      <SettingsCard
        title={t('Saved platform defaults')}
        description={t(
          'These are saved platform settings, not the current form draft or each user’s personal selections.'
        )}
      >
        <dl className='grid min-w-0 gap-4 text-sm md:grid-cols-3'>
          <div className='min-w-0 space-y-1'>
            <dt className='font-medium'>{t('Default chat model')}</dt>
            <dd className='break-words'>
              {props.config.default_chat_model ||
                t(
                  'No platform chat model is set. Clients following platform policy use the first available chat model in the configured order.'
                )}
            </dd>
            <dd className='text-muted-foreground text-xs'>
              {t(
                'The initial built-in chat model is deepseek-v4-flash; the account catalog and access still determine availability.'
              )}
            </dd>
          </div>
          {media.isPending && (
            <div className='md:col-span-2'>
              <LoadingState message={t('Loading image and video defaults')} />
            </div>
          )}
          {media.isError && !media.data && (
            <div className='md:col-span-2'>
              <ErrorState
                title={t('Failed to load image and video defaults')}
                action={
                  <Button
                    type='button'
                    variant='outline'
                    size='sm'
                    onClick={() => void media.refetch()}
                  >
                    {t('Retry image and video defaults')}
                  </Button>
                }
              />
            </div>
          )}
          {media.data && (
            <>
              <div className='min-w-0 space-y-1'>
                <dt className='font-medium'>{t('Default image model')}</dt>
                <dd className='break-words'>
                  {media.data.default_image_model ||
                    t(
                      'No platform image model is set. Use the first model in the account image catalog.'
                    )}
                </dd>
              </div>
              <div className='min-w-0 space-y-1'>
                <dt className='font-medium'>{t('Default video model')}</dt>
                <dd className='break-words'>
                  {media.data.default_video_model ||
                    t(
                      'No platform video model is set. Use MiniMax-H3, with 768P when no resolution is specified.'
                    )}
                </dd>
              </div>
            </>
          )}
        </dl>
        <p className='text-muted-foreground mt-4 text-xs'>
          {t(
            'Explicit user choices and saved personal preferences take priority. Existing conversations and accepted generation tasks keep their original model. Unavailable saved media choices require re-selection.'
          )}
        </p>
      </SettingsCard>
    </section>
  )
}
