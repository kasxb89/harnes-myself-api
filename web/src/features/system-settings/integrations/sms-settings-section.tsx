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

import { SettingsForm } from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useResetForm } from '../hooks/use-reset-form'
import { useUpdateOption } from '../hooks/use-update-option'

const smsSchema = z.object({
  SmsAliyunAccessKeyId: z.string(),
  SmsAliyunAccessKeySecret: z.string(),
  SmsAliyunSignName: z.string(),
  SmsAliyunTemplateCode: z.string(),
})

type SmsFormValues = z.infer<typeof smsSchema>

type SmsSettingsSectionProps = {
  defaultValues: SmsFormValues
}

export function SmsSettingsSection({ defaultValues }: SmsSettingsSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()

  const form = useForm<SmsFormValues>({
    resolver: zodResolver(smsSchema),
    defaultValues,
  })

  useResetForm(form, defaultValues)

  const onSubmit = async (values: SmsFormValues) => {
    const sanitized: SmsFormValues = {
      SmsAliyunAccessKeyId: values.SmsAliyunAccessKeyId.trim(),
      SmsAliyunAccessKeySecret: values.SmsAliyunAccessKeySecret.trim(),
      SmsAliyunSignName: values.SmsAliyunSignName.trim(),
      SmsAliyunTemplateCode: values.SmsAliyunTemplateCode.trim(),
    }
    const initial: SmsFormValues = {
      SmsAliyunAccessKeyId: defaultValues.SmsAliyunAccessKeyId.trim(),
      SmsAliyunAccessKeySecret: defaultValues.SmsAliyunAccessKeySecret.trim(),
      SmsAliyunSignName: defaultValues.SmsAliyunSignName.trim(),
      SmsAliyunTemplateCode: defaultValues.SmsAliyunTemplateCode.trim(),
    }

    const updates: Array<{ key: string; value: string }> = []

    // The secret is never returned by the options endpoint, so its field starts
    // blank on every load; a blank value therefore means "keep the stored one".
    for (const key of [
      'SmsAliyunAccessKeyId',
      'SmsAliyunAccessKeySecret',
      'SmsAliyunSignName',
      'SmsAliyunTemplateCode',
    ] as const) {
      if (sanitized[key] === initial[key]) continue
      if (key === 'SmsAliyunAccessKeySecret' && sanitized[key] === '') continue
      updates.push({ key, value: sanitized[key] })
    }

    for (const update of updates) {
      await updateOption.mutateAsync(update)
    }
  }

  return (
    <SettingsSection title={t('Aliyun SMS')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)} autoComplete='off'>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending}
            saveLabel='Save SMS settings'
          />
          <FormField
            control={form.control}
            name='SmsAliyunAccessKeyId'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('AccessKey ID')}</FormLabel>
                <FormControl>
                  <Input autoComplete='off' {...field} />
                </FormControl>
                <FormDescription>
                  {t(
                    'The RAM user the gateway sends verification codes as. Reuse the account whose sign name and template are already approved.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='SmsAliyunAccessKeySecret'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('AccessKey Secret')}</FormLabel>
                <FormControl>
                  <Input
                    type='password'
                    placeholder={t('Enter new secret to update')}
                    autoComplete='new-password'
                    {...field}
                  />
                </FormControl>
                <FormDescription>
                  {t('Leave blank to keep the existing secret.')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='SmsAliyunSignName'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('SMS sign name')}</FormLabel>
                <FormControl>
                  <Input autoComplete='off' {...field} />
                </FormControl>
                <FormDescription>
                  {t('The approved SMS signature the messages are sent under.')}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />

          <FormField
            control={form.control}
            name='SmsAliyunTemplateCode'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('SMS template code')}</FormLabel>
                <FormControl>
                  <Input autoComplete='off' {...field} />
                </FormControl>
                <FormDescription>
                  {t(
                    'The approved template, which must carry a numeric code parameter.'
                  )}
                </FormDescription>
                <FormMessage />
              </FormItem>
            )}
          />
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
