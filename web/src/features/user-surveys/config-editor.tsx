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
import { useEffect, useState } from 'react'
import { useFieldArray, useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import { z } from 'zod'

import { ConfirmDialog } from '@/components/confirm-dialog'
import {
  StaticDataTable,
  type StaticDataTableColumn,
} from '@/components/data-table'
import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { hasPermission } from '@/lib/admin-permissions'
import { useAuthStore } from '@/stores/auth-store'

import {
  getSurveyConfiguration,
  saveSurveyConfiguration,
  type SurveyConfigOption,
} from './api'

const label = (limit: number) =>
  z
    .string()
    .refine(
      (value) =>
        value.trim().length > 0 &&
        [...value].length <= limit &&
        !/[\p{Cc}]/u.test(value)
    )
const schema = z.object({
  options: z
    .array(
      z.object({
        id: z.string().regex(/^[a-z][a-z0-9_]{0,47}$/),
        label: label(80),
        label_en: label(160),
        enabled: z.boolean(),
      })
    )
    .min(1)
    .max(64)
    .refine((rows) => rows.some((row) => row.enabled)),
})
type Values = z.infer<typeof schema>

export function SurveyConfigEditor(props: { onClose: () => void }) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const writable =
    !!user && user.role >= 10 && hasPermission(user, 'user_surveys', 'write')
  const cache = useQueryClient()
  const query = useQuery({
    queryKey: ['survey-configuration', user?.id],
    queryFn: ({ signal }) => getSurveyConfiguration(signal),
    retry: false,
    refetchOnWindowFocus: false,
  })
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: { options: [] },
  })
  const array = useFieldArray({
    control: form.control,
    name: 'options',
    keyName: 'formKey',
  })
  const [revision, setRevision] = useState('')
  const [confirm, setConfirm] = useState<'reload' | 'close' | null>(null)
  const [saveFailed, setSaveFailed] = useState(false)
  const dirty = form.formState.isDirty
  const mutation = useMutation({
    mutationFn: (values: Values) =>
      saveSurveyConfiguration(revision, values.options),
    onSuccess: (cfg) => {
      form.reset({ options: cfg.options })
      setRevision(cfg.config_revision)
      setSaveFailed(false)
      cache.setQueryData(['survey-configuration', user?.id], cfg)
      void cache.invalidateQueries({ queryKey: ['user-surveys'] })
      toast.success(t('Questionnaire options saved'))
    },
    onError: () => setSaveFailed(true),
  })
  useEffect(() => {
    if (query.data && !dirty) {
      form.reset({ options: query.data.options })
      setRevision(query.data.config_revision)
    }
  }, [query.data, dirty, form])
  const blocked = !writable || mutation.isPending
  const requestClose = () => {
    if (dirty) setConfirm('close')
    else props.onClose()
  }
  const reload = async () => {
    const result = await query.refetch()
    if (result.data) {
      form.reset({ options: result.data.options })
      setRevision(result.data.config_revision)
      setSaveFailed(false)
    }
    setConfirm(null)
  }
  const savedIDs = new Set(query.data?.options.map((option) => option.id))
  type Row = SurveyConfigOption & { formKey: string }
  const columns: StaticDataTableColumn<Row>[] = [
    {
      id: 'label',
      header: t('Chinese name'),
      cell: (_row, index) => (
        <div className='min-w-40'>
          <Input
            aria-label={`${t('Chinese name')} ${index + 1}`}
            {...form.register(`options.${index}.label`)}
            disabled={blocked}
            aria-invalid={!!form.formState.errors.options?.[index]?.label}
          />
        </div>
      ),
    },
    {
      id: 'label_en',
      header: t('English name'),
      cell: (_row, index) => (
        <div className='min-w-48'>
          <Input
            aria-label={`${t('English name')} ${index + 1}`}
            {...form.register(`options.${index}.label_en`)}
            disabled={blocked}
            aria-invalid={!!form.formState.errors.options?.[index]?.label_en}
          />
        </div>
      ),
    },
    {
      id: 'enabled',
      header: t('Enabled'),
      cell: (_row, index) => (
        <Checkbox
          aria-label={`${t('Enabled')} ${index + 1}`}
          checked={form.watch(`options.${index}.enabled`)}
          onCheckedChange={(value) =>
            form.setValue(`options.${index}.enabled`, !!value, {
              shouldDirty: true,
            })
          }
          disabled={blocked}
        />
      ),
    },
    {
      id: 'order',
      header: t('Order'),
      cell: (row, index) => (
        <div className='flex gap-1'>
          <Button
            size='sm'
            type='button'
            variant='ghost'
            aria-label={`${t('Move up')} ${index + 1}`}
            disabled={blocked || index === 0}
            onClick={() => array.move(index, index - 1)}
          >
            ↑
          </Button>
          <Button
            size='sm'
            type='button'
            variant='ghost'
            aria-label={`${t('Move down')} ${index + 1}`}
            disabled={blocked || index === array.fields.length - 1}
            onClick={() => array.move(index, index + 1)}
          >
            ↓
          </Button>
          {!savedIDs.has(row.id) && (
            <Button
              size='sm'
              type='button'
              variant='ghost'
              onClick={() => array.remove(index)}
              disabled={blocked}
            >
              {t('Remove')}
            </Button>
          )}
        </div>
      ),
    },
  ]
  return (
    <>
      <Dialog
        open
        onOpenChange={(open) => {
          if (!open) requestClose()
        }}
        title={t('Questionnaire options')}
        description={t(
          'Rename, reorder or disable scenarios. Submitted answers keep the names shown when they were filled in.'
        )}
        contentClassName='sm:max-w-5xl'
        contentHeight='65dvh'
        footer={
          <>
            <Button
              variant='outline'
              onClick={() => {
                if (dirty) setConfirm('reload')
                else void reload()
              }}
              disabled={mutation.isPending}
            >
              {t('Reload')}
            </Button>
            <Button
              disabled={blocked || !revision || !dirty}
              onClick={() =>
                void form.handleSubmit((values) => mutation.mutate(values))()
              }
            >
              {t('Save')}
            </Button>
          </>
        }
      >
        {query.isPending && <LoadingState />}
        {query.isError && (
          <ErrorState
            title={t('Failed to load questionnaire options')}
            onRetry={() => void query.refetch()}
          />
        )}
        {!query.isError && query.data && (
          <div className='space-y-3'>
            {!writable && (
              <p className='text-muted-foreground text-sm'>
                {t('You have read-only access to questionnaire options.')}
              </p>
            )}
            {saveFailed && (
              <Alert variant='destructive'>
                <AlertDescription>
                  {t(
                    'Options were not saved. Your draft is preserved. If another administrator changed the options, reload and review before saving again.'
                  )}
                </AlertDescription>
              </Alert>
            )}
            {Object.keys(form.formState.errors).length > 0 && (
              <Alert variant='destructive'>
                <AlertDescription>
                  {t(
                    'Enter both names for every option, enable at least one option and keep at most 64 options.'
                  )}
                </AlertDescription>
              </Alert>
            )}
            <StaticDataTable
              columns={columns}
              data={array.fields}
              getRowKey={(row) => row.formKey}
            />
            <Button
              type='button'
              variant='outline'
              disabled={blocked || array.fields.length >= 64}
              onClick={() =>
                array.append({
                  id: `scene_${crypto.randomUUID().replaceAll('-', '')}`,
                  label: '',
                  label_en: '',
                  enabled: true,
                })
              }
            >
              {t('Add scenario')}
            </Button>
            <p className='text-muted-foreground text-sm'>
              {t(
                'Saved scenarios can be disabled. Historical answers are retained.'
              )}
            </p>
          </div>
        )}
      </Dialog>
      <ConfirmDialog
        open={confirm !== null}
        onOpenChange={(open) => {
          if (!open) setConfirm(null)
        }}
        title={t('Discard unsaved questionnaire changes?')}
        desc={t(
          'Your unsaved changes will be cleared. Saved answers will remain unchanged.'
        )}
        confirmText={confirm === 'reload' ? t('Reload') : t('Close')}
        handleConfirm={() => {
          if (confirm === 'reload') void reload()
          else props.onClose()
        }}
      />
    </>
  )
}
