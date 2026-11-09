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
import { Link } from '@tanstack/react-router'
import { memo } from 'react'
import type { UseFormReturn } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Button, buttonVariants } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
} from '@/components/ui/form'
import { Switch } from '@/components/ui/switch'

type ModelFormValues = {
  ModelPrice: string
  ModelRatio: string
  CacheRatio: string
  CreateCacheRatio: string
  CompletionRatio: string
  ImageRatio: string
  AudioRatio: string
  AudioCompletionRatio: string
  ExposeRatioEnabled: boolean
  BillingMode: string
  BillingExpr: string
  PluginBillingExpr: string
}

type ModelRatioFormProps = {
  form: UseFormReturn<ModelFormValues>
  savedValues: ModelFormValues
  onSave: (values: ModelFormValues) => Promise<void>
  onReset: () => void
  isSaving: boolean
  isResetting: boolean
  variant?: 'default' | 'unset'
}

// Model tariffs have one editor. This legacy route retains only the independent
// public-ratio visibility setting and navigation to that editor.
export const ModelRatioForm = memo(function ModelRatioForm({
  form,
  onSave,
  isSaving,
  variant = 'default',
}: ModelRatioFormProps) {
  const { t } = useTranslation()
  return (
    <div className='flex min-w-0 flex-col items-start gap-6'>
      <Link
        className={buttonVariants({ variant: 'outline' })}
        to='/client-model-config'
        search={{ tab: 'unified' }}
      >
        {t('Open unified model configuration')}
      </Link>
      {variant !== 'unset' && (
        <Form {...form}>
          <form
            className='flex w-full max-w-xl flex-col items-start gap-4'
            onSubmit={form.handleSubmit(onSave)}
          >
            <FormField
              control={form.control}
              name='ExposeRatioEnabled'
              render={({ field }) => (
                <FormItem className='flex w-full items-center justify-between gap-4'>
                  <div className='space-y-1'>
                    <FormLabel>{t('Expose ratio API')}</FormLabel>
                    <FormDescription>
                      {t(
                        'Allow clients to query configured prices via `/api/ratio`.'
                      )}
                    </FormDescription>
                  </div>
                  <FormControl>
                    <Switch
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                </FormItem>
              )}
            />
            <Button
              type='submit'
              disabled={isSaving || !form.formState.isDirty}
            >
              {isSaving ? t('Saving...') : t('Save')}
            </Button>
          </form>
        </Form>
      )}
    </div>
  )
})
