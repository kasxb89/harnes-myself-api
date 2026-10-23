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
import { useEffect, useRef, useState } from 'react'
import { Controller, useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import {
  Field,
  FieldDescription,
  FieldError,
  FieldGroup,
  FieldLabel,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { AssetUpload } from '@/features/creation-content/components/asset-upload'
import { getServerErrorMessage } from '@/lib/server-error-message'

import {
  SettingsForm,
  SettingsSwitchField,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import {
  loadCustomerService,
  saveCustomerService,
} from './customer-service-api'
import {
  CUSTOMER_SERVICE_QUERY_KEY,
  customerServiceSchema,
  EMPTY_CUSTOMER_SERVICE,
  type CustomerService,
} from './lib/customer-service'

export function CustomerServiceSection() {
  const { t } = useTranslation()
  const client = useQueryClient()
  const [uploading, setUploading] = useState(false)
  const pending = useRef(false)
  const query = useQuery({
    queryKey: CUSTOMER_SERVICE_QUERY_KEY,
    queryFn: ({ signal }) => loadCustomerService(signal),
    meta: { errorToast: false },
  })
  const form = useForm<CustomerService>({
    resolver: zodResolver(customerServiceSchema),
    defaultValues: EMPTY_CUSTOMER_SERVICE,
  })
  const { isDirty, errors } = form.formState
  const value = form.watch()
  const mutation = useMutation({
    mutationFn: saveCustomerService,
    meta: { errorToast: false },
    onMutate: () =>
      client.cancelQueries({ queryKey: CUSTOMER_SERVICE_QUERY_KEY }),
    onSuccess: (saved) => {
      form.reset(saved)
      client.setQueryData(CUSTOMER_SERVICE_QUERY_KEY, saved)
      void client.invalidateQueries({ queryKey: CUSTOMER_SERVICE_QUERY_KEY })
      void client.invalidateQueries({ queryKey: ['status'] })
      toast.success(t('Customer service settings saved'))
    },
  })
  const busy = uploading || mutation.isPending

  useEffect(() => {
    if (query.data && !isDirty && !pending.current) form.reset(query.data)
  }, [query.data, form, isDirty])

  const onSubmit = async (next: CustomerService): Promise<void> => {
    if (pending.current || busy || !query.data) return
    pending.current = true
    try {
      await mutation.mutateAsync(next)
    } catch {
      // The mutation error remains visible; keep every unsaved form value.
    } finally {
      pending.current = false
    }
  }

  if (query.isPending) {
    return (
      <div role='status'>
        <LoadingState message={t('Loading customer service settings...')} />
      </div>
    )
  }
  if (!query.data) {
    return (
      <ErrorState
        title={t('Unable to load customer service settings')}
        onRetry={() => void query.refetch()}
      />
    )
  }

  return (
    <SettingsSection title={t('Customer service')}>
      <p className='text-muted-foreground text-sm'>
        {t(
          'Configure the QR code shown when desktop users open customer service. Save all changes together.'
        )}
      </p>
      <SettingsForm
        aria-label={t('Customer service settings')}
        onSubmit={form.handleSubmit(onSubmit)}
      >
        <SettingsPageFormActions
          onSave={form.handleSubmit(onSubmit)}
          isSaving={mutation.isPending}
          isSaveDisabled={!isDirty || uploading}
          saveLabel='Save customer service settings'
        />
        {mutation.isError && (
          <Alert variant='destructive'>
            <AlertTitle>
              {t('Unable to save customer service settings')}
            </AlertTitle>
            <AlertDescription>
              {getServerErrorMessage(
                mutation.error,
                t(
                  'Saving failed. Your draft is preserved. Retry saving when ready.'
                )
              )}
            </AlertDescription>
          </Alert>
        )}
        {query.isError && (
          <ErrorState
            title={t('Unable to load customer service settings')}
            onRetry={() => void query.refetch()}
          />
        )}
        <fieldset disabled={busy} className='min-w-0'>
          <FieldGroup>
            <Controller
              control={form.control}
              name='enabled'
              render={({ field }) => (
                <SettingsSwitchField
                  controlId='customer-service-enabled'
                  checked={field.value}
                  onCheckedChange={field.onChange}
                  disabled={busy}
                  label={t('Enable customer service QR code')}
                  description={t(
                    'Enable only after uploading a QR code. Disabled settings remain saved as a draft.'
                  )}
                />
              )}
            />
            <Controller
              control={form.control}
              name='qr_code_url'
              render={({ field }) => (
                <Field data-invalid={!!errors.qr_code_url}>
                  <AssetUpload
                    kind='image'
                    label={t('Customer service QR code')}
                    value={
                      field.value
                        ? field.value.slice('/api/creation/assets/'.length)
                        : null
                    }
                    onChange={(id) =>
                      field.onChange(id ? `/api/creation/assets/${id}` : '')
                    }
                    onBusyChange={setUploading}
                    disabled={mutation.isPending}
                    aria-invalid={!!errors.qr_code_url}
                    aria-describedby={
                      errors.qr_code_url
                        ? 'customer-service-qr-error'
                        : undefined
                    }
                  />
                  <FieldDescription>
                    {t(
                      'Upload PNG, JPEG or WebP, up to 10 MiB and 16 megapixels. External image links are not accepted.'
                    )}
                  </FieldDescription>
                  <FieldError id='customer-service-qr-error'>
                    {errors.qr_code_url?.message &&
                      t(errors.qr_code_url.message)}
                  </FieldError>
                </Field>
              )}
            />
            <Field data-invalid={!!errors.title} data-disabled={busy}>
              <FieldLabel htmlFor='customer-service-title'>
                {t('Customer service title (optional)')}
              </FieldLabel>
              <Input
                id='customer-service-title'
                maxLength={64}
                aria-invalid={!!errors.title}
                aria-describedby='customer-service-title-error'
                {...form.register('title')}
              />
              <FieldError id='customer-service-title-error'>
                {errors.title?.message && t(errors.title.message)}
              </FieldError>
            </Field>
            <Field data-invalid={!!errors.description} data-disabled={busy}>
              <FieldLabel htmlFor='customer-service-description'>
                {t('Customer service description (optional)')}
              </FieldLabel>
              <Textarea
                id='customer-service-description'
                maxLength={256}
                rows={3}
                aria-invalid={!!errors.description}
                aria-describedby='customer-service-description-help customer-service-description-error'
                {...form.register('description')}
              />
              <FieldDescription id='customer-service-description-help'>
                {t(
                  'Plain text only, without line breaks or control characters.'
                )}
              </FieldDescription>
              <FieldError id='customer-service-description-error'>
                {errors.description?.message && t(errors.description.message)}
              </FieldError>
            </Field>
            {!value.qr_code_url && (
              <FieldDescription>
                {t(
                  'No customer service QR code configured. Users will see an unconfigured message.'
                )}
              </FieldDescription>
            )}
          </FieldGroup>
        </fieldset>
      </SettingsForm>
    </SettingsSection>
  )
}
