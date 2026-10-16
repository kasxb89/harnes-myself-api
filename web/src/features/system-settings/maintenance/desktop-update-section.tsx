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
import { Switch } from '@/components/ui/switch'

import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'
import type { OperationsSettings } from '../types'

const feedFields = ['mac_arm64_url', 'mac_x64_url', 'win_x64_url'] as const

function hasControlCharacters(value: string): boolean {
  return [...value].some((character) => {
    const code = character.charCodeAt(0)
    return code < 32 || (code >= 127 && code <= 159)
  })
}

function isUpdateFeedURL(value: string): boolean {
  if (!value) return true
  if (!/^https?:\/\//i.test(value)) return false
  if (/[\s\\?#]/.test(value) || hasControlCharacters(value)) return false
  if (/%(?:0[0-9a-f]|1[0-9a-f]|7f|5c|2f)/i.test(value)) return false
  try {
    const url = new URL(value)
    if (url.username || url.password || !url.hostname) return false
    const path = decodeURIComponent(url.pathname)
    if (path.includes('\\') || hasControlCharacters(path)) {
      return false
    }
    if (
      /\.(exe|dmg|zip|yml|yaml|blockmap|json)$/i.test(
        url.pathname.replace(/\/+$/, '')
      )
    ) {
      return false
    }
    if (url.protocol === 'https:') return true
    const loopback =
      url.hostname === 'localhost' ||
      url.hostname === '[::1]' ||
      url.hostname === '127.0.0.1'
    return url.protocol === 'http:' && loopback
  } catch {
    return false
  }
}

type DesktopUpdateSectionProps = {
  defaultValues: Pick<
    OperationsSettings,
    | 'desktop_update.enabled'
    | 'desktop_update.mac_arm64_url'
    | 'desktop_update.mac_x64_url'
    | 'desktop_update.win_x64_url'
  >
}

export function DesktopUpdateSection(props: DesktopUpdateSectionProps) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const feedLabels = {
    mac_arm64_url: t('macOS Apple Silicon update feed'),
    mac_x64_url: t('macOS Intel update feed'),
    win_x64_url: t('Windows x64 update feed'),
  }
  const urlSchema = z
    .string()
    .trim()
    .max(2048, { message: t('Invalid desktop update feed URL') })
    .refine(isUpdateFeedURL, {
      message: t('Invalid desktop update feed URL'),
    })
  const schema = z
    .object({
      desktop_update: z.object({
        enabled: z.boolean(),
        mac_arm64_url: urlSchema,
        mac_x64_url: urlSchema,
        win_x64_url: urlSchema,
      }),
    })
    .superRefine((values, context) => {
      if (
        values.desktop_update.enabled &&
        !feedFields.some((key) => values.desktop_update[key])
      ) {
        context.addIssue({
          code: 'custom',
          path: ['desktop_update', 'mac_arm64_url'],
          message: t(
            'Add at least one platform update feed before enabling client updates.'
          ),
        })
      }
    })
  type FormValues = z.infer<typeof schema>
  const form = useForm<FormValues>({
    resolver: zodResolver(schema),
    defaultValues: {
      desktop_update: {
        enabled: props.defaultValues['desktop_update.enabled'],
        mac_arm64_url: props.defaultValues['desktop_update.mac_arm64_url'],
        mac_x64_url: props.defaultValues['desktop_update.mac_x64_url'],
        win_x64_url: props.defaultValues['desktop_update.win_x64_url'],
      },
    },
  })

  const onSubmit = async (values: FormValues): Promise<void> => {
    const settings = values.desktop_update
    const baseline = form.formState.defaultValues?.desktop_update
    const updates: [string, string | boolean][] = feedFields.map((key) => [
      `desktop_update.${key}`,
      settings[key],
    ])
    // Save feed locations before enabling, and disable before clearing feeds.
    const enabledUpdate: [string, boolean] = [
      'desktop_update.enabled',
      settings.enabled,
    ]
    if (settings.enabled) updates.push(enabledUpdate)
    else updates.unshift(enabledUpdate)
    try {
      for (const [key, value] of updates) {
        const field = key.slice(
          'desktop_update.'.length
        ) as keyof typeof settings
        if (baseline?.[field] === value) continue
        await updateOption.mutateAsync({ key, value })
      }
      form.reset(values)
    } catch {
      // The shared mutation displays the server error; keep unsaved input.
    }
  }

  return (
    <SettingsSection title={t('Client updates')}>
      <Form {...form}>
        <SettingsForm noValidate onSubmit={form.handleSubmit(onSubmit)}>
          <p className='text-muted-foreground text-sm'>
            {t('Manage update sources for the product desktop app.')}
          </p>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            onReset={() => form.reset()}
            isSaving={form.formState.isSubmitting}
            saveLabel='Save client update settings'
          />
          <FormField
            control={form.control}
            name='desktop_update.enabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Enable client updates')}</FormLabel>
                  <FormDescription>
                    {t(
                      'The desktop app reads these sources whenever it checks for updates.'
                    )}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                    disabled={form.formState.isSubmitting}
                  />
                </FormControl>
                <FormMessage />
              </SettingsSwitchItem>
            )}
          />
          <p className='text-muted-foreground text-sm'>
            {t(
              'Enter a feed directory containing nightly update metadata and installers, not an installer file URL. Leave unsupported platforms empty. These URLs are public; do not include secrets. HTTPS is required except for HTTP loopback during development.'
            )}
          </p>
          {feedFields.map((key) => (
            <FormField
              key={key}
              control={form.control}
              name={`desktop_update.${key}`}
              render={({ field }) => (
                <FormItem data-settings-form-span='full'>
                  <FormLabel>{feedLabels[key]}</FormLabel>
                  <FormControl>
                    <Input
                      {...field}
                      type='url'
                      autoComplete='off'
                      spellCheck={false}
                      disabled={form.formState.isSubmitting}
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          ))}
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
