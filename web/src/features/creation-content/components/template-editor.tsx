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
import { useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Alert, AlertTitle, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Field,
  FieldGroup,
  FieldLabel,
  FieldDescription,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { getServerErrorMessage } from '@/lib/server-error-message'

import { templateSchema, type Template, type Category } from '../lib/schema'
import { AssetUpload } from './asset-upload'
import { LocalizedField } from './localized-field'

type Props = {
  value: Template
  categories: Category[]
  onSave: (value: Template) => Promise<void>
  onClose: () => void
}
export function TemplateEditor(props: Props) {
  const { t } = useTranslation()
  const [uploading, setUploading] = useState(false)
  const [savingIntent, setSavingIntent] = useState<Template['status'] | null>(
    null
  )
  const [saveError, setSaveError] = useState<string | null>(null)
  const pending = useRef(false)
  const busy = uploading || savingIntent !== null
  const form = useForm<Template>({
    resolver: zodResolver(templateSchema),
    defaultValues: props.value,
  })
  const value = form.watch()
  const errors = form.formState.errors
  const draftLabel =
    props.value.status === 'published' ? t('Move to draft') : t('Save draft')
  const submit = async (status: Template['status']): Promise<void> => {
    if (pending.current || uploading) return
    pending.current = true
    setSavingIntent(status)
    setSaveError(null)
    form.clearErrors('coverAssetId')
    try {
      await form.handleSubmit(
        async (next) => {
          if (status === 'published' && !next.coverAssetId) {
            form.setError('coverAssetId', {
              message: t('Published templates require a cover'),
            })
            return
          }
          await props.onSave({ ...next, status })
          props.onClose()
        },
        () => setSaveError(t('Check the required text and length limits'))
      )()
    } catch (error: unknown) {
      setSaveError(
        getServerErrorMessage(error, t('Unable to save creation template'))
      )
    } finally {
      pending.current = false
      setSavingIntent(null)
    }
  }

  return (
    <Dialog
      open
      title={t('Edit creation template')}
      onOpenChange={(open) => {
        if (!open && !busy && !pending.current) props.onClose()
      }}
      showCloseButton={!busy}
      footer={
        <>
          <Button
            type='button'
            variant='outline'
            disabled={busy}
            onClick={() => {
              if (!busy && !pending.current) props.onClose()
            }}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='button'
            variant='outline'
            disabled={busy}
            onClick={() => {
              void submit('draft')
            }}
          >
            {savingIntent === 'draft' ? t('Saving...') : draftLabel}
          </Button>
          <Button type='submit' form='creation-template-form' disabled={busy}>
            {savingIntent === 'published' ? t('Publishing...') : t('Publish')}
          </Button>
        </>
      }
    >
      <form
        id='creation-template-form'
        aria-label={t('Edit creation template')}
        onSubmit={(event) => {
          event.preventDefault()
          void submit('published')
        }}
      >
        {saveError && (
          <Alert variant='destructive' className='mb-4' role='alert'>
            <AlertTitle>{t('Unable to save creation template')}</AlertTitle>
            <AlertDescription>{saveError}</AlertDescription>
          </Alert>
        )}
        <p className='text-muted-foreground mb-4 text-sm'>
          {t(
            'Publish or save a draft directly. Other valid changes on this page are saved together.'
          )}
        </p>
        <fieldset disabled={busy} className='min-w-0'>
          <FieldGroup>
            <LocalizedField
              label={t('Title')}
              value={value.title}
              onChange={(next) => form.setValue('title', next)}
              maxLength={200}
              required
              invalid={!!errors.title}
            />
            <LocalizedField
              label={t('Description')}
              value={value.description}
              onChange={(next) => form.setValue('description', next)}
              maxLength={2000}
              multiline
              required
              invalid={!!errors.description}
            />
            <LocalizedField
              label={t('Prompt')}
              value={value.prompt}
              onChange={(next) => form.setValue('prompt', next)}
              maxLength={7000}
              multiline
              required
              invalid={!!errors.prompt}
            />
            <Field>
              <FieldLabel htmlFor='creation-template-category'>
                {t('Category')}
              </FieldLabel>
              <NativeSelect
                id='creation-template-category'
                value={value.categoryId ?? ''}
                onChange={(event) =>
                  form.setValue('categoryId', event.target.value || null)
                }
              >
                <NativeSelectOption value=''>
                  {t('Uncategorized')}
                </NativeSelectOption>
                {props.categories.map((category) => (
                  <NativeSelectOption key={category.id} value={category.id}>
                    {category.label.zh}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </Field>
            <div className='grid gap-4 sm:grid-cols-2'>
              <Field>
                <FieldLabel htmlFor='creation-template-order'>
                  {t('Sort order')}
                </FieldLabel>
                <Input
                  id='creation-template-order'
                  type='number'
                  min={-100000}
                  max={100000}
                  {...form.register('sortOrder', { valueAsNumber: true })}
                />
              </Field>
            </div>
            <AssetUpload
              kind='image'
              value={value.coverAssetId}
              onChange={(next) => form.setValue('coverAssetId', next)}
              onBusyChange={setUploading}
            />
            {errors.coverAssetId && (
              <FieldDescription role='alert'>
                {t('Published templates require a cover')}
              </FieldDescription>
            )}
            <AssetUpload
              kind='video'
              value={value.demoVideoAssetId}
              onChange={(next) => form.setValue('demoVideoAssetId', next)}
              onBusyChange={setUploading}
            />
          </FieldGroup>
        </fieldset>
      </form>
    </Dialog>
  )
}
