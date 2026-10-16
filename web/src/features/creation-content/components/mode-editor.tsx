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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { EmptyState } from '@/components/empty-state'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  CardDescription,
} from '@/components/ui/card'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Switch } from '@/components/ui/switch'

import type { ModeContent, Template, Mode } from '../lib/schema'
import { LocalizedField } from './localized-field'
import { TemplateEditor } from './template-editor'

type Props = {
  mode: Mode
  value: ModeContent
  onChange: (value: ModeContent) => void
  onSaveTemplate: (value: Template) => Promise<void>
  invalid: boolean
}
type Removal = { kind: 'category' | 'template' | 'preset'; id: string }
export function ModeEditor(props: Props) {
  const { t } = useTranslation()
  const [editing, setEditing] = useState<Template | null>(null)
  const [removal, setRemoval] = useState<Removal | null>(null)
  const value = props.value
  const remove = () => {
    if (!removal) return
    if (removal.kind === 'category') {
      props.onChange({
        ...value,
        categories: value.categories.filter((item) => item.id !== removal.id),
        templates: value.templates.map((item) =>
          item.categoryId === removal.id ? { ...item, categoryId: null } : item
        ),
      })
    }
    if (removal.kind === 'template') {
      props.onChange({
        ...value,
        templates: value.templates.filter((item) => item.id !== removal.id),
      })
    }
    if (removal.kind === 'preset') {
      props.onChange({
        ...value,
        presets: value.presets.filter((item) => item.id !== removal.id),
      })
    }
    setRemoval(null)
  }
  return (
    <div className='grid min-w-0 gap-6'>
      <Card>
        <CardHeader>
          <CardTitle>{t('Creation templates')}</CardTitle>
          <CardDescription>
            {t('Only published templates with a cover are shown to users.')}
          </CardDescription>
        </CardHeader>
        <CardContent className='grid gap-4'>
          {value.templates.length === 0 && (
            <EmptyState
              title={t('No creation templates yet')}
              description={t(
                'Upload your own cover and write a prompt to publish the first template.'
              )}
              className='min-h-36'
            />
          )}
          <div className='grid gap-3 md:grid-cols-2 xl:grid-cols-3'>
            {value.templates.map((item) => (
              <Card key={item.id} size='sm'>
                {item.coverAssetId && (
                  <img
                    src={`/api/creation/assets/${item.coverAssetId}`}
                    alt={item.title.zh}
                    loading='lazy'
                    className='aspect-video w-full object-cover'
                  />
                )}
                <CardHeader>
                  <CardTitle className='break-words'>{item.title.zh}</CardTitle>
                  <CardDescription className='line-clamp-2'>
                    {item.description.zh}
                  </CardDescription>
                </CardHeader>
                <CardContent className='flex flex-wrap gap-2'>
                  <Badge variant='secondary'>
                    {item.status === 'published' ? t('Published') : t('Draft')}
                  </Badge>
                  <Button
                    type='button'
                    size='sm'
                    variant='outline'
                    onClick={() => setEditing(item)}
                    aria-label={`${t('Edit template')}: ${item.title.zh}`}
                  >
                    {t('Edit')}
                  </Button>
                  <Button
                    type='button'
                    size='sm'
                    variant='outline'
                    onClick={() =>
                      setRemoval({ kind: 'template', id: item.id })
                    }
                    aria-label={`${t('Delete template')}: ${item.title.zh}`}
                  >
                    {t('Delete')}
                  </Button>
                </CardContent>
              </Card>
            ))}
          </div>
          <Button
            type='button'
            variant='outline'
            className='justify-self-start'
            disabled={value.templates.length >= 500}
            onClick={() =>
              setEditing({
                id: `t_${crypto.randomUUID()}`,
                categoryId: null,
                title: { zh: '' },
                description: { zh: '' },
                prompt: { zh: '' },
                coverAssetId: null,
                demoVideoAssetId: null,
                sortOrder: value.templates.length,
                status: 'draft',
              })
            }
          >
            {t('Add template')}
          </Button>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>{t('Creation page copy')}</CardTitle>
          <CardDescription>
            {t(
              'Empty page copy uses the product default. Published content appears in the product automatically.'
            )}
          </CardDescription>
        </CardHeader>
        <CardContent>
          <FieldGroup>
            <LocalizedField
              label={t('Page title')}
              value={value.title}
              onChange={(next) => props.onChange({ ...value, title: next })}
              maxLength={200}
            />
            <LocalizedField
              label={t('Page description')}
              value={value.description}
              onChange={(next) =>
                props.onChange({ ...value, description: next })
              }
              maxLength={2000}
              multiline
            />
            <LocalizedField
              label={t('Prompt placeholder')}
              value={value.promptPlaceholder}
              onChange={(next) =>
                props.onChange({ ...value, promptPlaceholder: next })
              }
              maxLength={1000}
              multiline
            />
          </FieldGroup>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>{t('Creation categories')}</CardTitle>
          <CardDescription>
            {t('Disabled categories hide their templates from the product.')}
          </CardDescription>
        </CardHeader>
        <CardContent className='grid gap-4'>
          {value.categories.length === 0 && (
            <p className='text-muted-foreground'>{t('No categories yet')}</p>
          )}
          {value.categories.map((item) => (
            <FieldGroup key={item.id} className='rounded-lg border p-4'>
              <LocalizedField
                label={t('Category name')}
                value={item.label}
                onChange={(next) =>
                  props.onChange({
                    ...value,
                    categories: value.categories.map((entry) =>
                      entry.id === item.id ? { ...entry, label: next } : entry
                    ),
                  })
                }
                maxLength={100}
                required
                invalid={props.invalid && !item.label.zh.trim()}
              />
              <div className='flex flex-wrap items-end gap-4'>
                <Field className='w-32'>
                  <FieldLabel htmlFor={`${item.id}-order`}>
                    {t('Sort order')}
                  </FieldLabel>
                  <Input
                    id={`${item.id}-order`}
                    type='number'
                    min={-100000}
                    max={100000}
                    value={item.sortOrder}
                    onChange={(event) =>
                      props.onChange({
                        ...value,
                        categories: value.categories.map((entry) =>
                          entry.id === item.id
                            ? {
                                ...entry,
                                sortOrder: Number(event.target.value),
                              }
                            : entry
                        ),
                      })
                    }
                  />
                </Field>
                <Field orientation='horizontal' className='w-auto'>
                  <FieldLabel htmlFor={`${item.id}-enabled`}>
                    {t('Enabled')}
                  </FieldLabel>
                  <Switch
                    id={`${item.id}-enabled`}
                    checked={item.enabled}
                    onCheckedChange={(enabled) =>
                      props.onChange({
                        ...value,
                        categories: value.categories.map((entry) =>
                          entry.id === item.id ? { ...entry, enabled } : entry
                        ),
                      })
                    }
                  />
                </Field>
                <Button
                  type='button'
                  variant='outline'
                  onClick={() => setRemoval({ kind: 'category', id: item.id })}
                >
                  {t('Delete category')}
                </Button>
              </div>
            </FieldGroup>
          ))}
          <Button
            type='button'
            variant='outline'
            className='justify-self-start'
            disabled={value.categories.length >= 100}
            onClick={() =>
              props.onChange({
                ...value,
                categories: [
                  ...value.categories,
                  {
                    id: `c_${crypto.randomUUID()}`,
                    label: { zh: '' },
                    sortOrder: value.categories.length,
                    enabled: true,
                  },
                ],
              })
            }
          >
            {t('Add category')}
          </Button>
        </CardContent>
      </Card>
      <Card>
        <CardHeader>
          <CardTitle>
            {props.mode === 'image'
              ? t('Style presets')
              : t('Camera and style presets')}
          </CardTitle>
          <CardDescription>
            {t('Selecting a preset appends its prompt text in the product.')}
          </CardDescription>
        </CardHeader>
        <CardContent className='grid gap-4'>
          {value.presets.length === 0 && (
            <p className='text-muted-foreground'>{t('No presets yet')}</p>
          )}
          {value.presets.map((item) => (
            <FieldGroup key={item.id} className='rounded-lg border p-4'>
              <LocalizedField
                label={t('Preset name')}
                value={item.label}
                onChange={(next) =>
                  props.onChange({
                    ...value,
                    presets: value.presets.map((entry) =>
                      entry.id === item.id ? { ...entry, label: next } : entry
                    ),
                  })
                }
                maxLength={100}
                required
                invalid={props.invalid && !item.label.zh.trim()}
              />
              <LocalizedField
                label={t('Appended prompt')}
                value={item.promptSuffix}
                onChange={(next) =>
                  props.onChange({
                    ...value,
                    presets: value.presets.map((entry) =>
                      entry.id === item.id
                        ? { ...entry, promptSuffix: next }
                        : entry
                    ),
                  })
                }
                maxLength={2000}
                multiline
                required
                invalid={props.invalid && !item.promptSuffix.zh.trim()}
              />
              <div className='flex flex-wrap items-end gap-4'>
                <Field className='w-32'>
                  <FieldLabel htmlFor={`${item.id}-order`}>
                    {t('Sort order')}
                  </FieldLabel>
                  <Input
                    id={`${item.id}-order`}
                    type='number'
                    min={-100000}
                    max={100000}
                    value={item.sortOrder}
                    onChange={(event) =>
                      props.onChange({
                        ...value,
                        presets: value.presets.map((entry) =>
                          entry.id === item.id
                            ? {
                                ...entry,
                                sortOrder: Number(event.target.value),
                              }
                            : entry
                        ),
                      })
                    }
                  />
                </Field>
                <Field className='w-auto'>
                  <FieldLabel htmlFor={`${item.id}-status`}>
                    {t('Publication status')}
                  </FieldLabel>
                  <NativeSelect
                    id={`${item.id}-status`}
                    value={item.status}
                    onChange={(event) =>
                      props.onChange({
                        ...value,
                        presets: value.presets.map((entry) =>
                          entry.id === item.id
                            ? {
                                ...entry,
                                status:
                                  event.target.value === 'published'
                                    ? 'published'
                                    : 'draft',
                              }
                            : entry
                        ),
                      })
                    }
                  >
                    <NativeSelectOption value='draft'>
                      {t('Draft')}
                    </NativeSelectOption>
                    <NativeSelectOption value='published'>
                      {t('Published')}
                    </NativeSelectOption>
                  </NativeSelect>
                </Field>
                <Button
                  type='button'
                  variant='outline'
                  onClick={() => setRemoval({ kind: 'preset', id: item.id })}
                >
                  {t('Delete preset')}
                </Button>
              </div>
            </FieldGroup>
          ))}
          <Button
            type='button'
            variant='outline'
            className='justify-self-start'
            disabled={value.presets.length >= 100}
            onClick={() =>
              props.onChange({
                ...value,
                presets: [
                  ...value.presets,
                  {
                    id: `p_${crypto.randomUUID()}`,
                    label: { zh: '' },
                    promptSuffix: { zh: '' },
                    sortOrder: value.presets.length,
                    status: 'draft',
                  },
                ],
              })
            }
          >
            {t('Add preset')}
          </Button>
        </CardContent>
      </Card>
      {editing && (
        <TemplateEditor
          key={editing.id}
          value={editing}
          categories={value.categories}
          onClose={() => setEditing(null)}
          onSave={props.onSaveTemplate}
        />
      )}
      <ConfirmDialog
        open={!!removal}
        onOpenChange={(open) => {
          if (!open) setRemoval(null)
        }}
        title={t('Remove creation item?')}
        desc={t(
          'This changes the draft only. Save to update the product. Uploaded files are retained.'
        )}
        destructive
        confirmText={t('Delete')}
        handleConfirm={remove}
      />
    </div>
  )
}
