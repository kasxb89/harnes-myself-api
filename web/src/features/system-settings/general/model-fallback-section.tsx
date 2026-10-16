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
import i18next from 'i18next'
import type { Resolver } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import * as z from 'zod'

import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'

import { FormDirtyIndicator } from '../components/form-dirty-indicator'
import { FormNavigationGuard } from '../components/form-navigation-guard'
import {
  SettingsForm,
  SettingsFormGrid,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useSettingsForm } from '../hooks/use-settings-form'
import { useUpdateOption } from '../hooks/use-update-option'

/**
 * Whether a value is a fallback table: empty, or a JSON object whose every value
 * is a list of model names. The backend rejects anything else, so the form says
 * so before the request leaves.
 * @param value - the raw textarea content.
 * @returns true when the value may be saved.
 */
function isFallbackTable(value: string): boolean {
  const trimmed = value.trim()
  if (trimmed === '') return true
  let parsed: unknown
  try {
    parsed = JSON.parse(trimmed)
  } catch {
    return false
  }
  if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
    return false
  }
  return Object.values(parsed).every(
    (candidates) =>
      Array.isArray(candidates) &&
      candidates.every(
        (candidate) => typeof candidate === 'string' && candidate.trim() !== ''
      )
  )
}

const fallbackSchema = z.object({
  model_fallback_setting: z.object({
    model_fallback_map: z.string().refine(isFallbackTable, {
      error: () =>
        i18next.t(
          'Enter a JSON object mapping each model to the models that may serve it instead'
        ),
    }),
    model_fallback_max_candidates: z.coerce
      .number({ error: () => i18next.t('Please enter a valid number') })
      .int({ error: () => i18next.t('Must be an integer') })
      .min(0, { error: () => i18next.t('Must be greater than or equal to 0') }),
  }),
})

export type ModelFallbackFormValues = z.infer<typeof fallbackSchema>

export function ModelFallbackSection({
  defaultValues,
}: {
  defaultValues: ModelFallbackFormValues
}) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const { form, handleSubmit, isDirty, isSubmitting } =
    useSettingsForm<ModelFallbackFormValues>({
      resolver: zodResolver(fallbackSchema) as Resolver<
        ModelFallbackFormValues,
        unknown,
        ModelFallbackFormValues
      >,
      defaultValues,
      onSubmit: async (_data, changedFields) => {
        for (const [key, value] of Object.entries(changedFields)) {
          await updateOption.mutateAsync({
            key,
            value: value as string | number | boolean,
          })
        }
      },
    })

  return (
    <SettingsSection title={t('Model Fallback')}>
      <FormNavigationGuard when={isDirty} />
      <Form {...form}>
        <SettingsForm onSubmit={handleSubmit}>
          <SettingsPageFormActions
            onSave={handleSubmit}
            isSaving={updateOption.isPending || isSubmitting}
          />
          <FormDirtyIndicator isDirty={isDirty} />
          <SettingsFormGrid>
            <FormField
              control={form.control}
              name='model_fallback_setting.model_fallback_map'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Model fallback table')}</FormLabel>
                  <FormControl>
                    <Textarea
                      rows={6}
                      placeholder='{"glm-5.2":["glm-5.1","kimi-k2.6"]}'
                      value={field.value ?? ''}
                      onChange={field.onChange}
                      name={field.name}
                      onBlur={field.onBlur}
                      ref={field.ref}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'JSON object mapping a model to the models that may serve a request instead, in the order to try them. A request whose model has no usable channel — or whose attempts all fail upstream — moves on to the next model, and is billed at the model that actually served it. Leave empty to keep every request on the model it asked for.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='model_fallback_setting.model_fallback_max_candidates'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Fallback candidates per request')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={0}
                      value={field.value ?? ''}
                      onChange={field.onChange}
                      name={field.name}
                      onBlur={field.onBlur}
                      ref={field.ref}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'How many fallback models one request may try. Set to 0 to disable fallback; defaults to 3.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </SettingsFormGrid>
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
