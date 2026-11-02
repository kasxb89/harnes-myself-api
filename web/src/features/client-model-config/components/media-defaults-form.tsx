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
import { zodResolver } from '@hookform/resolvers/zod'
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { z } from 'zod'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Combobox } from '@/components/ui/combobox'
import {
  Form,
  FormField,
  FormItem,
  FormLabel,
  FormControl,
  FormDescription,
  FormMessage,
} from '@/components/ui/form'
import { FormDirtyIndicator } from '@/features/system-settings/components/form-dirty-indicator'
import { SettingsCard } from '@/features/system-settings/components/settings-card'
import { SettingsPageFormActions } from '@/features/system-settings/components/settings-page-context'
import { getModelDisplayName } from '@/lib/model-display-name'

import { getClientMediaDefaults, saveClientMediaDefaults } from '../api'
import type { ClientModelOption } from '../types'

const schema = z.object({
  schema_version: z.literal(1),
  default_image_model: z.string(),
  default_video_model: z.string(),
})
type Values = z.infer<typeof schema>
export function ClientMediaDefaultsSection(props: {
  models: ClientModelOption[]
  readOnly: boolean
  active: boolean
  onDirtyChange: (dirty: boolean) => void
}) {
  const { t } = useTranslation()
  const cache = useQueryClient()
  const query = useQuery({
    queryKey: ['client-media-defaults'],
    queryFn: getClientMediaDefaults,
  })
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: {
      schema_version: 1,
      default_image_model: '',
      default_video_model: '',
    },
  })
  const dirty = form.formState.isDirty
  const mutation = useMutation({
    mutationFn: saveClientMediaDefaults,
    onSuccess: (value) => {
      form.reset(value)
      cache.setQueryData(['client-media-defaults'], value)
      void cache.invalidateQueries({ queryKey: ['status'] })
      toast.success(t('Client model configuration saved'))
    },
  })
  const onDirtyChange = props.onDirtyChange
  useEffect(() => {
    onDirtyChange(dirty)
  }, [dirty, onDirtyChange])
  useEffect(() => {
    if (query.data && !dirty && !mutation.isPending) form.reset(query.data)
  }, [query.data, dirty, mutation.isPending, form])
  const fields = [
    {
      name: 'default_image_model',
      label: 'Default image model',
      eligible: 'image_eligible',
      fallback:
        'No platform image model is set. Use the first model in the account image catalog.',
    },
    {
      name: 'default_video_model',
      label: 'Default video model',
      eligible: 'video_eligible',
      fallback:
        'No platform video model is set. Use {{model}}, with 768P when no resolution is specified.',
    },
  ] as const
  const invalid = fields.some(
    ({ name, eligible }) =>
      form.watch(name) !== '' &&
      !props.models.some(
        (m) => m.id === form.watch(name) && m[eligible] === true
      )
  )
  if (query.isPending) return <LoadingState />
  if (!query.data) {
    return (
      <ErrorState
        title={t('Failed to load client model configuration')}
        onRetry={() => void query.refetch()}
      />
    )
  }
  return (
    <Form {...form}>
      <form
        className='space-y-4'
        onSubmit={form.handleSubmit((value) => {
          if (!props.readOnly && !invalid) mutation.mutate(value)
        })}
      >
        <FormDirtyIndicator isDirty={dirty} />
        {props.active && !props.readOnly && (
          <SettingsPageFormActions
            onSave={form.handleSubmit((value) => {
              if (!invalid) mutation.mutate(value)
            })}
            onReset={() => form.reset()}
            isSaving={mutation.isPending}
            isSaveDisabled={!dirty || invalid}
            isResetDisabled={!dirty}
          />
        )}
        <Alert>
          <AlertDescription>
            {t(
              'The selectors include enabled original channels and YCAPI models with matching media capabilities. Missing or disabled routes cannot be selected as a platform default.'
            )}
          </AlertDescription>
        </Alert>
        {invalid && (
          <Alert variant='destructive'>
            <AlertDescription>
              {t(
                'Some saved models are unavailable. Remove them from the order or choose another default before saving.'
              )}
            </AlertDescription>
          </Alert>
        )}
        {fields.map(({ name, label, eligible, fallback }) => (
          <SettingsCard key={name} title={t(label)}>
            <FormField
              control={form.control}
              name={name}
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t(label)}</FormLabel>
                  <FormControl>
                    <Combobox
                      ref={field.ref}
                      aria-label={t(label)}
                      className='w-full'
                      options={[
                        { value: '', label: t('Keep client default') },
                        ...props.models
                          .filter((m) => m[eligible] === true)
                          .map((m) => ({
                            value: m.id,
                            label: getModelDisplayName(
                              m.id,
                              m.display_name,
                              m.display_name || m.id
                            ),
                            description: m.id,
                          })),
                      ]}
                      value={field.value}
                      onValueChange={(value) => field.onChange(value ?? '')}
                      onBlur={field.onBlur}
                      disabled={props.readOnly || mutation.isPending}
                    />
                  </FormControl>
                  {field.value === '' && (
                    <FormDescription>
                      {t(fallback, { model: getModelDisplayName('MiniMax-H3') })}
                    </FormDescription>
                  )}
                  <FormMessage />
                </FormItem>
              )}
            />
          </SettingsCard>
        ))}
      </form>
    </Form>
  )
}
