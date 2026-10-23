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
import { useId } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
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
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'

import {
  presentationEditorSchema,
  presentationEntryToForm,
  presentationFormToEntry,
  type PresentationEditorValues,
} from '../lib/presentation'
import type { ClientModelPresentationEntry } from '../types'

type PresentationDialogProps = {
  modelId: string
  entry: ClientModelPresentationEntry
  onApply: (entry: ClientModelPresentationEntry) => void
  onClose: () => void
}

export function PresentationDialog(props: PresentationDialogProps) {
  const { t } = useTranslation()
  const formId = useId()
  const form = useForm<PresentationEditorValues>({
    resolver: zodResolver(presentationEditorSchema),
    defaultValues: presentationEntryToForm(props.entry),
  })
  const values = form.watch()
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) props.onClose()
      }}
      title={t('Edit model description and badge')}
      description={<span className='break-all'>{props.modelId}</span>}
      footer={
        <>
          <Button type='button' variant='outline' onClick={props.onClose}>
            {t('Cancel')}
          </Button>
          <Button
            type='submit'
            form={formId}
            disabled={!form.formState.isDirty}
          >
            {t('Apply to draft')}
          </Button>
        </>
      }
    >
      <Form {...form}>
        <form
          id={formId}
          noValidate
          onSubmit={form.handleSubmit((value) =>
            props.onApply(presentationFormToEntry(value))
          )}
        >
          <FieldGroup>
            {(['description', 'badge'] as const).map((fieldName) => (
              <Field key={fieldName}>
                <FormField
                  control={form.control}
                  name={`${fieldName}.mode`}
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>
                        {fieldName === 'description'
                          ? t('Description display')
                          : t('Badge display')}
                      </FormLabel>
                      <FormControl>
                        <NativeSelect {...field} className='w-full'>
                          <NativeSelectOption value='default'>
                            {t('Follow client default')}
                          </NativeSelectOption>
                          <NativeSelectOption value='custom'>
                            {t('Show custom text')}
                          </NativeSelectOption>
                          <NativeSelectOption value='hidden'>
                            {t('Do not display')}
                          </NativeSelectOption>
                        </NativeSelect>
                      </FormControl>
                      <FormDescription>
                        {t(
                          'Each field is independent. Client defaults may vary by client version.'
                        )}
                      </FormDescription>
                      <FormMessage />
                    </FormItem>
                  )}
                />
                {values[fieldName].mode === 'custom' && (
                  <FormField
                    control={form.control}
                    name={`${fieldName}.text`}
                    render={({ field }) => (
                      <FormItem>
                        <FormLabel>
                          {fieldName === 'description'
                            ? t('Custom description')
                            : t('Custom badge')}
                        </FormLabel>
                        <FormControl>
                          {fieldName === 'description' ? (
                            <Textarea {...field} rows={4} />
                          ) : (
                            <Input {...field} />
                          )}
                        </FormControl>
                        <FormDescription>
                          {fieldName === 'description'
                            ? t(
                                'Plain text, up to 500 characters. Line breaks are allowed.'
                              )
                            : t(
                                'One plain-text badge, up to 32 characters on a single line.'
                              )}
                        </FormDescription>
                        <FormMessage />
                      </FormItem>
                    )}
                  />
                )}
              </Field>
            ))}
          </FieldGroup>
        </form>
      </Form>
    </Dialog>
  )
}
