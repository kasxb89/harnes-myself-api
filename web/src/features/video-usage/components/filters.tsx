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

import { Button } from '@/components/ui/button'
import { ComboboxInput } from '@/components/ui/combobox-input'
import { Field, FieldLabel } from '@/components/ui/field'
import { CompactDateTimeRangePicker } from '@/features/usage-logs/components/compact-date-time-range-picker'

import type { VideoUsageDraft } from '../lib/filters'
import {
  channelOptionLabel,
  userOptionLabel,
  videoProviderName,
} from '../lib/format'
import {
  videoProviders,
  type VideoChannel,
  type VideoProvider,
  type VideoUser,
} from '../schema'

export function VideoUsageFilters(props: {
  value: VideoUsageDraft
  models: string[]
  users: VideoUser[]
  channels: VideoChannel[]
  providers: VideoProvider[]
  loading: boolean
  invalid: boolean
  onChange: (value: VideoUsageDraft) => void
  onApply: () => void
  onReset: () => void
}) {
  const { t } = useTranslation()
  return (
    <form
      aria-label={t('Video usage filters')}
      className='grid items-end gap-3 rounded-xl border p-4 sm:grid-cols-2 xl:grid-cols-3'
      onSubmit={(event) => {
        event.preventDefault()
        if (!props.loading && !props.invalid) props.onApply()
      }}
    >
      <Field aria-label={t('History period')}>
        <FieldLabel>{t('History period')}</FieldLabel>
        <CompactDateTimeRangePicker
          start={props.value.start}
          end={props.value.end}
          onChange={(range) =>
            props.onChange({
              ...props.value,
              start: range.start,
              end: range.end
                ? new Date(Math.min(Date.now(), range.end.getTime()))
                : undefined,
            })
          }
        />
      </Field>
      <Field>
        <FieldLabel htmlFor='video-usage-user'>{t('User')}</FieldLabel>
        <ComboboxInput
          id='video-usage-user'
          aria-label={t('User')}
          allowCustomValue={false}
          openOnFocus={false}
          value={props.value.userId}
          options={[
            { value: '', label: t('All users') },
            ...props.users.map((user) => ({
              value: String(user.id),
              label: userOptionLabel(user),
            })),
          ]}
          placeholder={t('Search users...')}
          onValueChange={(userId) => props.onChange({ ...props.value, userId })}
        />
      </Field>
      <Field>
        <FieldLabel htmlFor='video-usage-model'>{t('Model')}</FieldLabel>
        <ComboboxInput
          id='video-usage-model'
          aria-label={t('Model')}
          allowCustomValue
          openOnFocus={false}
          value={props.value.model}
          options={[
            { value: '', label: t('All models') },
            ...props.models.map((model) => ({ value: model, label: model })),
          ]}
          placeholder={t('Search models...')}
          onValueChange={(model) => props.onChange({ ...props.value, model })}
        />
      </Field>
      <Field>
        <FieldLabel htmlFor='video-usage-source'>
          {t('Client source')}
        </FieldLabel>
        <ComboboxInput
          id='video-usage-source'
          aria-label={t('Client source')}
          allowCustomValue={false}
          openOnFocus={false}
          value={props.value.source}
          options={[
            { value: 'all', label: t('All sources') },
            { value: 'desktop', label: t('Desktop client declaration') },
            { value: 'unknown', label: t('Unknown') },
          ]}
          onValueChange={(source) => {
            if (
              source === 'all' ||
              source === 'desktop' ||
              source === 'unknown'
            ) {
              props.onChange({ ...props.value, source })
            }
          }}
        />
      </Field>
      <Field>
        <FieldLabel htmlFor='video-usage-channel'>{t('Channel')}</FieldLabel>
        <ComboboxInput
          id='video-usage-channel'
          aria-label={t('Channel')}
          allowCustomValue={false}
          openOnFocus={false}
          value={props.value.channelId}
          options={[
            { value: '', label: t('All channels') },
            ...props.channels.map((channel) => ({
              value: String(channel.id),
              label: channelOptionLabel(channel, t('Unknown channel')),
            })),
          ]}
          onValueChange={(channelId) =>
            props.onChange({ ...props.value, channelId })
          }
        />
      </Field>
      <Field>
        <FieldLabel htmlFor='video-usage-provider'>
          {t('Provider (current settings)')}
        </FieldLabel>
        <ComboboxInput
          id='video-usage-provider'
          aria-label={t('Provider (current settings)')}
          allowCustomValue={false}
          openOnFocus={false}
          value={props.value.provider}
          options={[
            { value: 'all', label: t('All providers') },
            ...props.providers.map((provider) => ({
              value: provider,
              label: videoProviderName(provider) || t('Unknown'),
            })),
          ]}
          onValueChange={(provider) => {
            if (
              provider === 'all' ||
              videoProviders.some((item) => item === provider)
            ) {
              props.onChange({
                ...props.value,
                provider: provider as 'all' | VideoProvider,
              })
            }
          }}
        />
      </Field>
      <p className='text-muted-foreground text-xs sm:col-span-2 xl:col-span-3'>
        {t(
          'Providers reflect current channel settings, not historical supplier identity or independent accounts.'
        )}
      </p>
      <div className='flex gap-2'>
        <Button type='submit' disabled={props.invalid || props.loading}>
          {t('Apply filters')}
        </Button>
        <Button type='button' variant='outline' onClick={props.onReset}>
          {t('Reset')}
        </Button>
      </div>
    </form>
  )
}
