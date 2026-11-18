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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useFieldArray, useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'

import {
  configSchema,
  loadDemandConfig,
  saveDemandConfig,
  type DemandConfig,
} from './api'

export function DemandSettings({
  accountId,
  canWrite,
}: {
  accountId: number
  canWrite: boolean
}) {
  const query = useQuery({
    queryKey: ['demand-config', accountId],
    queryFn: ({ signal }) => loadDemandConfig(signal),
    retry: false,
  })
  if (query.isPending) return <LoadingState />
  if (!query.data) {
    return (
      <ErrorState
        onRetry={() => {
          void query.refetch()
        }}
      />
    )
  }
  return (
    <SettingsForm
      key={`${accountId}:${query.data.revision}`}
      initial={query.data}
      accountId={accountId}
      canWrite={canWrite}
    />
  )
}
function SettingsForm({
  initial,
  accountId,
  canWrite,
}: {
  initial: DemandConfig
  accountId: number
  canWrite: boolean
}) {
  const { t } = useTranslation()
  const cache = useQueryClient()
  const form = useForm<DemandConfig>({
    defaultValues: initial,
    resolver: zodResolver(configSchema),
  })
  const categories = useFieldArray({
    control: form.control,
    name: 'config.categories',
  })
  const save = useMutation({
    mutationFn: saveDemandConfig,
    retry: false,
    onSuccess: (value) => {
      form.reset(value)
      cache.setQueryData(['demand-config', accountId], value)
    },
  })
  const fields = [
    'max_image_bytes',
    'max_video_bytes',
    'max_attachments',
    'chunk_bytes',
    'upload_ttl_seconds',
  ] as const
  const names = {
    max_image_bytes: 'Maximum image size (bytes)',
    max_video_bytes: 'Maximum video size (bytes)',
    max_attachments: 'Attachments per message',
    chunk_bytes: 'Upload chunk size (bytes)',
    upload_ttl_seconds: 'Upload session lifetime (seconds)',
  } as const
  return (
    <Form {...form}>
      <form
        className='max-w-4xl space-y-6'
        onSubmit={form.handleSubmit((value) => save.mutate(value))}
      >
        <fieldset disabled={!canWrite || save.isPending} className='space-y-6'>
          <FormField
            control={form.control}
            name='config.enabled'
            render={({ field }) => (
              <FormItem className='flex min-h-11 items-center gap-3'>
                <FormControl>
                  <Checkbox
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                </FormControl>
                <FormLabel>{t('Enable demand publishing')}</FormLabel>
              </FormItem>
            )}
          />
          <section className='space-y-3' aria-label={t('Categories')}>
            <div className='flex items-center justify-between'>
              <h2 className='font-medium'>{t('Categories')}</h2>
              <Button
                type='button'
                variant='outline'
                onClick={() =>
                  categories.append({ id: '', name: '', enabled: true })
                }
              >
                {t('Add category')}
              </Button>
            </div>
            {categories.fields.map((category, index) => (
              <div
                key={category.id}
                className='grid items-end gap-3 border-b pb-3 sm:grid-cols-[1fr_2fr_auto_auto]'
              >
                <FormField
                  control={form.control}
                  name={`config.categories.${index}.id`}
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Category ID')}</FormLabel>
                      <FormControl>
                        <Input {...field} />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name={`config.categories.${index}.name`}
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t('Category name')}</FormLabel>
                      <FormControl>
                        <Input {...field} />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                <FormField
                  control={form.control}
                  name={`config.categories.${index}.enabled`}
                  render={({ field }) => (
                    <FormItem className='flex min-h-11 items-center gap-2'>
                      <FormControl>
                        <Checkbox
                          checked={field.value}
                          onCheckedChange={field.onChange}
                        />
                      </FormControl>
                      <FormLabel>{t('Enabled')}</FormLabel>
                    </FormItem>
                  )}
                />
                <Button
                  type='button'
                  variant='ghost'
                  onClick={() => categories.remove(index)}
                  aria-label={`${t('Remove category')} ${category.name}`}
                >
                  {t('Remove')}
                </Button>
              </div>
            ))}
          </section>
          <section className='space-y-4' aria-label={t('Attachment rules')}>
            <h2 className='font-medium'>{t('Attachment rules')}</h2>
            <div className='grid gap-4 sm:grid-cols-2'>
              {fields.map((name) => (
                <FormField
                  key={name}
                  control={form.control}
                  name={`config.upload_rules.${name}`}
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>{t(names[name])}</FormLabel>
                      <FormControl>
                        <Input
                          type='number'
                          min={1}
                          {...field}
                          onChange={(e) =>
                            field.onChange(e.target.valueAsNumber)
                          }
                        />
                      </FormControl>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              ))}
            </div>
            <FormField
              control={form.control}
              name='config.upload_rules.allowed_mime_types'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Allowed formats')}</FormLabel>
                  <div className='flex flex-wrap gap-4'>
                    {[
                      'image/jpeg',
                      'image/png',
                      'image/webp',
                      'video/mp4',
                      'video/webm',
                    ].map((type) => (
                      <label
                        key={type}
                        className='flex min-h-11 items-center gap-2 text-sm'
                      >
                        <Checkbox
                          checked={field.value.includes(type)}
                          onCheckedChange={(checked) =>
                            field.onChange(
                              checked
                                ? [...field.value, type]
                                : field.value.filter((v) => v !== type)
                            )
                          }
                        />
                        {type}
                      </label>
                    ))}
                  </div>
                  <FormMessage />
                </FormItem>
              )}
            />
          </section>
          {canWrite && (
            <Button
              type='submit'
              disabled={!form.formState.isDirty || save.isPending}
            >
              {t(save.isPending ? 'Saving...' : 'Save settings')}
            </Button>
          )}
        </fieldset>
        {save.isError && (
          <p role='alert' className='text-destructive text-sm'>
            {t(
              'Settings were not saved. Reload the latest revision before retrying; your edits are retained.'
            )}
          </p>
        )}
      </form>
    </Form>
  )
}
