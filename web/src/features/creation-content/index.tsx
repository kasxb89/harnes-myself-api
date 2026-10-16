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
import { useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout/components/section-page-layout'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertTitle, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Tabs, TabsList, TabsTrigger, TabsContent } from '@/components/ui/tabs'
import { handleServerError } from '@/lib/handle-server-error'

import { loadContent, saveContent } from './api'
import { ModeEditor } from './components/mode-editor'
import {
  contentSchema,
  type CreationContent,
  type Mode,
  type Template,
} from './lib/schema'

const queryKey = ['creation-content', 'admin'] as const
type SaveRequest = {
  content: CreationContent
  intent: 'content' | 'draft' | 'published'
}
export function CreationContentPage() {
  const { t } = useTranslation()
  const query = useQuery({
    queryKey,
    queryFn: loadContent,
    refetchOnWindowFocus: false,
  })
  if (query.isPending) return <LoadingState />
  if (!query.data) {
    return (
      <ErrorState
        description={t('Unable to load creation content')}
        onRetry={() => {
          void query.refetch()
        }}
      />
    )
  }
  return <ContentEditor initial={query.data} />
}
function ContentEditor(props: { initial: CreationContent }) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const form = useForm<CreationContent>({
    defaultValues: props.initial,
    resolver: zodResolver(contentSchema),
  })
  const value = form.watch()
  const [reloadOpen, setReloadOpen] = useState(false)
  const [invalid, setInvalid] = useState(false)
  const [conflict, setConflict] = useState(false)
  const reload = useMutation({
    mutationFn: loadContent,
    onSuccess: (next) => {
      form.reset(next)
      queryClient.setQueryData(queryKey, next)
      setConflict(false)
      setInvalid(false)
      setReloadOpen(false)
    },
    onError: (error: unknown) => handleServerError(error),
  })
  const save = useMutation({
    mutationFn: (request: SaveRequest) => saveContent(request.content),
    onSuccess: (next, request) => {
      form.reset(next)
      queryClient.setQueryData(queryKey, next)
      setConflict(false)
      setInvalid(false)
      if (request.intent === 'published') {
        toast.success(t('Creation template published'))
      } else if (request.intent === 'draft') {
        toast.success(t('Creation template saved as draft'))
      } else {
        toast.success(
          t('Creation content saved and published updates are live')
        )
      }
    },
    onError: (error: unknown) => {
      if (typeof error === 'object' && error !== null && 'response' in error) {
        const response = error.response as { status?: number }
        if (response.status === 409) setConflict(true)
      }
      handleServerError(error)
    },
  })
  const busy = save.isPending || reload.isPending
  const submit = form.handleSubmit(
    (next) => save.mutate({ content: next, intent: 'content' }),
    () => setInvalid(true)
  )
  const saveTemplate = async (
    mode: Mode,
    template: Template
  ): Promise<void> => {
    const current = form.getValues()
    const existing = current[mode].templates.some(
      (item) => item.id === template.id
    )
    const candidate = {
      ...current,
      [mode]: {
        ...current[mode],
        templates: existing
          ? current[mode].templates.map((item) =>
              item.id === template.id ? template : item
            )
          : [...current[mode].templates, template],
      },
    }
    const parsed = contentSchema.safeParse(candidate)
    if (!parsed.success) {
      setInvalid(true)
      throw new Error(
        t(
          'Other unsaved content is incomplete. Complete required fields on this page before saving the template. Your template edits are retained.'
        )
      )
    }
    await save.mutateAsync({ content: parsed.data, intent: template.status })
  }
  return (
    <SectionPageLayout fixedContent stackActionsOnMobile>
      <SectionPageLayout.Title>
        {t('AI Creation Management')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <Button
          variant='outline'
          disabled={busy}
          onClick={() => setReloadOpen(true)}
        >
          {t('Reload content')}
        </Button>
        <Button
          disabled={busy || !form.formState.isDirty}
          onClick={() => {
            void submit()
          }}
        >
          {save.isPending ? t('Saving...') : t('Save content')}
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <section
          aria-label={t('AI Creation Management')}
          tabIndex={0}
          className='focus-visible:ring-ring/50 h-full min-h-0 overflow-y-auto focus-visible:ring-2 focus-visible:outline-none'
        >
          <div className='mx-auto grid w-full max-w-7xl gap-6 pb-4'>
            <p className='text-muted-foreground'>
              {t(
                'Manage the text, categories, templates and presets users see in AI Creation.'
              )}
            </p>
            {form.formState.isDirty && (
              <p className='text-muted-foreground text-sm' role='status'>
                {t('You have unpublished changes')}
              </p>
            )}
            {invalid && (
              <Alert variant='destructive'>
                <AlertTitle>{t('Check creation content')}</AlertTitle>
                <AlertDescription>
                  {t(
                    'Complete required Chinese text, use valid sort orders and add a cover before publishing templates.'
                  )}
                </AlertDescription>
              </Alert>
            )}
            {conflict && (
              <Alert variant='destructive'>
                <AlertTitle>
                  {t('Creation content changed; reload before saving')}
                </AlertTitle>
                <AlertDescription>
                  {t(
                    'Your edits are retained. Copy any changes you want to keep before reloading the latest version.'
                  )}
                </AlertDescription>
              </Alert>
            )}
            <fieldset disabled={busy} className='min-w-0'>
              <Tabs defaultValue='image'>
                <TabsList aria-label={t('Creation mode')}>
                  <TabsTrigger value='image'>{t('Images')}</TabsTrigger>
                  <TabsTrigger value='video'>{t('Videos')}</TabsTrigger>
                </TabsList>
                <TabsContent value='image'>
                  <ModeEditor
                    mode='image'
                    onSaveTemplate={(next) => saveTemplate('image', next)}
                    value={value.image}
                    invalid={invalid}
                    onChange={(next) =>
                      form.setValue('image', next, { shouldDirty: true })
                    }
                  />
                </TabsContent>
                <TabsContent value='video'>
                  <ModeEditor
                    mode='video'
                    onSaveTemplate={(next) => saveTemplate('video', next)}
                    value={value.video}
                    invalid={invalid}
                    onChange={(next) =>
                      form.setValue('video', next, { shouldDirty: true })
                    }
                  />
                </TabsContent>
              </Tabs>
            </fieldset>
          </div>
        </section>
        <ConfirmDialog
          open={reloadOpen}
          onOpenChange={setReloadOpen}
          title={t('Reload creation content?')}
          desc={t(
            'Reloading replaces unsaved edits with the latest saved content.'
          )}
          confirmText={t('Reload content')}
          isLoading={reload.isPending}
          handleConfirm={() => reload.mutate()}
        />
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
