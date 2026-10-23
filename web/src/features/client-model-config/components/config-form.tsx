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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useEffect } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Combobox } from '@/components/ui/combobox'
import { Field, FieldGroup } from '@/components/ui/field'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { FormDirtyIndicator } from '@/features/system-settings/components/form-dirty-indicator'
import { FormNavigationGuard } from '@/features/system-settings/components/form-navigation-guard'
import { SettingsCard } from '@/features/system-settings/components/settings-card'
import { SettingsPageFormActions } from '@/features/system-settings/components/settings-page-context'
import { handleServerError } from '@/lib/handle-server-error'

import { saveClientModelConfig } from '../api'
import {
  buildClientModelOrder,
  clientModelConfigFormSchema,
  clientModelConfigToForm,
  type ClientModelConfigFormValues,
} from '../lib/schema'
import type { ClientModelConfig, ClientModelOption } from '../types'
import { ModelOrderEditor } from './model-order-editor'

type ConfigFormProps = {
  active?: boolean
  onDirtyChange?: (dirty: boolean) => void
  config: ClientModelConfig
  models: ClientModelOption[]
}

export function ClientModelConfigForm(props: ConfigFormProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const form = useForm<ClientModelConfigFormValues>({
    resolver: zodResolver(clientModelConfigFormSchema),
    defaultValues: clientModelConfigToForm(props.config),
  })
  const save = useMutation({
    mutationFn: saveClientModelConfig,
    onSuccess: (config) => {
      form.reset(clientModelConfigToForm(config))
      queryClient.setQueryData(['client-model-config'], config)
      void queryClient.invalidateQueries({ queryKey: ['status'] })
      toast.success(t('Client model configuration saved'))
    },
    onError: (error) => handleServerError(error),
  })
  const isDirty = form.formState.isDirty
  const onDirtyChange = props.onDirtyChange
  useEffect(() => {
    onDirtyChange?.(isDirty)
  }, [isDirty, onDirtyChange])
  const reset = form.reset
  useEffect(() => {
    if (!isDirty && !save.isPending) {
      reset(clientModelConfigToForm(props.config))
    }
  }, [props.config, isDirty, reset, save.isPending])
  const defaultModel = form.watch('default_chat_model')
  const ranks = form.watch('model_ranks')
  const order = buildClientModelOrder(ranks, props.models)
  const invalidRanks = !clientModelConfigFormSchema.safeParse(form.watch())
    .success
  const unavailable = order.filter(
    (id) => !props.models.some((model) => model.id === id)
  )
  const invalidDefault =
    defaultModel !== '' &&
    !props.models.some(
      (model) => model.id === defaultModel && model.chat_eligible
    )
  const options = [
    { value: '', label: t('No default preference') },
    ...props.models
      .filter((model) => model.chat_eligible)
      .map((model) => ({
        value: model.id,
        label: model.id,
        description:
          model.display_name !== model.id ? model.display_name : undefined,
      })),
  ]
  const onSubmit = async (
    values: ClientModelConfigFormValues
  ): Promise<void> => {
    if (invalidDefault) {
      form.setError('default_chat_model', {
        message: t('Default client model must support chat'),
      })
      return
    }
    if (unavailable.length > 0) {
      form.setError('model_ranks', {
        message: t('Client model configuration contains an unavailable model'),
      })
      return
    }
    try {
      await save.mutateAsync({
        schema_version: 1,
        default_chat_model: values.default_chat_model,
        model_order: buildClientModelOrder(values.model_ranks, props.models),
      })
    } catch {
      // Keep the draft; the mutation displays the server error.
    }
  }

  return (
    <Form {...form}>
      <form
        noValidate
        onSubmit={form.handleSubmit(onSubmit)}
        className='min-w-0 space-y-4'
      >
        <FormDirtyIndicator isDirty={form.formState.isDirty} />
        <FormNavigationGuard
          when={!props.onDirtyChange && form.formState.isDirty}
        />
        {props.active !== false && (
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            onReset={() => form.reset()}
            resetLabel='Discard changes'
            saveLabel='Save client model configuration'
            isSaving={save.isPending}
            isSaveDisabled={
              !form.formState.isDirty ||
              invalidDefault ||
              unavailable.length > 0 ||
              invalidRanks
            }
            isResetDisabled={!form.formState.isDirty}
          />
        )}
        <Alert>
          <AlertDescription>
            {t(
              'Client preferences only apply to models each user can access. Existing conversations and explicit user choices are preserved.'
            )}
          </AlertDescription>
        </Alert>
        {(unavailable.length > 0 || invalidDefault) && (
          <Alert variant='destructive'>
            <AlertDescription>
              {t(
                'Some saved models are unavailable. Remove them from the order or choose another default before saving.'
              )}
            </AlertDescription>
          </Alert>
        )}
        <SettingsCard
          title={t('Default chat model')}
          description={t(
            'Used when a user has not chosen a chat model. If unavailable to that user, the client chooses an available model.'
          )}
        >
          <FieldGroup>
            <Field>
              <FormField
                control={form.control}
                name='default_chat_model'
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>{t('Default chat model')}</FormLabel>
                    <FormControl>
                      <Combobox
                        ref={field.ref}
                        options={options}
                        value={field.value}
                        onValueChange={(id) => field.onChange(id ?? '')}
                        onBlur={field.onBlur}
                        disabled={save.isPending}
                        aria-label={t('Default chat model')}
                        className='w-full'
                      />
                    </FormControl>
                    <FormDescription>
                      {t(
                        'Only chat-capable models can be selected as the default.'
                      )}
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />
            </Field>
          </FieldGroup>
        </SettingsCard>
        <SettingsCard
          title={t('Client model order')}
          description={t(
            'Enter sort numbers directly. Smaller numbers come first; blank models follow in catalog order. Equal numbers use catalog order.'
          )}
        >
          <FormField
            control={form.control}
            name='model_ranks'
            render={({ field }) => (
              <FormItem>
                <FormLabel className='sr-only'>
                  {t('Client model order')}
                </FormLabel>
                <ModelOrderEditor
                  value={field.value}
                  models={props.models}
                  defaultChatModel={defaultModel}
                  disabled={save.isPending}
                  onChange={field.onChange}
                />
                <FormMessage />
              </FormItem>
            )}
          />
        </SettingsCard>
      </form>
    </Form>
  )
}
