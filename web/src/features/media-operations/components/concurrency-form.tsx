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
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
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
import { Input } from '@/components/ui/input'
import { SettingsCard } from '@/features/system-settings/components/settings-card'

import {
  concurrencyToForm,
  concurrencyUpdate,
  mediaConcurrencyFormSchema,
  type MediaConcurrency,
  type MediaConcurrencyUpdate,
  type MediaConcurrencyFormValues,
} from '../concurrency-schema'
import { useMediaFormat } from '../lib/format'
import { MediaConcurrencyTable } from './concurrency-table'

export function MediaConcurrencyForm(props: {
  snapshot: MediaConcurrency
  canEdit: boolean
  busy: boolean
  conflict: boolean
  onSave: (request: MediaConcurrencyUpdate) => void
}) {
  const { t } = useTranslation()
  const format = useMediaFormat()
  const form = useForm<MediaConcurrencyFormValues>({
    resolver: zodResolver(mediaConcurrencyFormSchema),
    defaultValues: concurrencyToForm(props.snapshot),
  })
  const disabled = !props.canEdit || props.busy || props.conflict
  const submit = (values: MediaConcurrencyFormValues) => {
    if (disabled) return
    const request = concurrencyUpdate(props.snapshot, values)
    if (request.pool_overrides.length > 128) {
      form.setError('pools', {
        message: 'At most 128 pool overrides are allowed',
      })
      return
    }
    props.onSave(request)
  }
  const totals = { video: 0, image: 0 }
  props.snapshot.pools.forEach((pool) => {
    if (pool.enabled && pool.reconciled) {
      totals[pool.media_kind] += pool.effective_limit
    }
  })
  return (
    <Form {...form}>
      <form
        noValidate
        onSubmit={form.handleSubmit(submit)}
        className='min-w-0 space-y-4'
      >
        {!props.canEdit && (
          <Alert>
            <AlertDescription>
              {t('Only root administrators can change media concurrency.')}
            </AlertDescription>
          </Alert>
        )}
        <SettingsCard
          title={t('Default limit per upstream key')}
          description={t(
            'The same upstream key shares its limit across users, channels and models. Independent keys add their limits; four H3 keys at four each allow sixteen admissions.'
          )}
        >
          <FieldGroup className='sm:flex-row'>
            {(['video', 'image'] as const).map((kind) => (
              <Field key={kind}>
                <FormField
                  control={form.control}
                  name={kind}
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>
                        {kind === 'video'
                          ? t('Default video concurrency per key')
                          : t('Default image concurrency per key')}
                      </FormLabel>
                      <FormControl>
                        <Input
                          {...field}
                          inputMode='numeric'
                          disabled={disabled}
                          className='max-w-40'
                        />
                      </FormControl>
                      <FormDescription>
                        {kind === 'video'
                          ? t('Allowed range: 1–16')
                          : t('Allowed range: 1–64')}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </Field>
            ))}
          </FieldGroup>
        </SettingsCard>
        <SettingsCard
          title={t('Account pool configuration')}
          description={t(
            'A blank override inherits the default. Policy limits are gateway settings; unknown upstream limits remain unknown.'
          )}
        >
          <div
            className='mb-4 flex flex-wrap gap-x-6 gap-y-2 text-sm'
            aria-label={t('Enabled admission limits')}
          >
            <p>
              {t('Enabled video admission limit: {{count}}', {
                count: format.count(totals.video),
              })}
            </p>
            <p>
              {t('Enabled image admission limit: {{count}}', {
                count: format.count(totals.image),
              })}
            </p>
          </div>
          <MediaConcurrencyTable
            pools={props.snapshot.pools}
            form={form}
            disabled={disabled}
          />
          <FormField
            control={form.control}
            name='pools'
            render={() => (
              <FormItem>
                <FormMessage />
              </FormItem>
            )}
          />
        </SettingsCard>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Available slots are gateway admission slots, not a confirmation of upstream capacity. Lowering a limit preserves running and UNKNOWN tasks and blocks new submissions until slots are available.'
          )}
        </p>
        {props.canEdit && (
          <div className='flex flex-wrap gap-2'>
            <Button
              type='submit'
              disabled={disabled || !form.formState.isDirty}
            >
              {t('Save media concurrency')}
            </Button>
            <Button
              type='button'
              variant='outline'
              disabled={props.busy || !form.formState.isDirty}
              onClick={() => form.reset()}
            >
              {t('Discard changes')}
            </Button>
          </div>
        )}
      </form>
    </Form>
  )
}
