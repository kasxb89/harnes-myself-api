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
import { z } from 'zod'

import { CopyButton } from '@/components/copy-button'
import { StaticDataTable } from '@/components/data-table'
import { Dialog } from '@/components/dialog'
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
import { Textarea } from '@/components/ui/textarea'
import { formatNumber } from '@/lib/format'

import {
  attachmentLink,
  engineerSchema,
  loadDemandConfig,
  loadEngineers,
  saveEngineer,
  uploadDemandFile,
  type DemandConfig,
  type Engineer,
} from './api'

const formSchema = engineerSchema.extend({
  user_id: z.number().int().positive(),
  display_name: z.string().min(1).max(100),
  capacity: z.number().int().min(1).max(1000),
  introduction: z.string().max(2000),
})
const blankEngineer: Engineer = {
  id: '',
  user_id: 0,
  display_name: '',
  avatar_attachment_id: '',
  category_ids: [],
  introduction: '',
  service_hours: '',
  published: false,
  enabled: true,
  accepting: true,
  capacity: 10,
  sort: 0,
  revision: 0,
  presence: 'offline',
  can_start: false,
  open_demand_count: 0,
}

export function DemandEngineers({
  accountId,
  canWrite,
}: {
  accountId: number
  canWrite: boolean
}) {
  const { t } = useTranslation()
  const cache = useQueryClient()
  const [editing, setEditing] = useState<Engineer | null>(null)
  const query = useQuery({
    queryKey: ['demand-engineers', accountId],
    queryFn: ({ signal }) => loadEngineers(signal),
    retry: false,
  })
  const config = useQuery({
    queryKey: ['demand-config', accountId],
    queryFn: ({ signal }) => loadDemandConfig(signal),
    retry: false,
  })
  const save = useMutation({
    mutationFn: saveEngineer,
    retry: false,
    onSuccess: () => {
      setEditing(null)
      void cache.invalidateQueries({
        queryKey: ['demand-engineers', accountId],
      })
    },
  })
  if (query.isPending || config.isPending) return <LoadingState />
  if (!query.data || !config.data) {
    return (
      <ErrorState
        onRetry={() => {
          void query.refetch()
          void config.refetch()
        }}
      />
    )
  }
  return (
    <section className='space-y-4' aria-label={t('Engineers')}>
      <div className='flex flex-wrap items-center justify-between gap-3'>
        <CopyButton
          value={`${window.location.origin}/engineer-workspace`}
          size='sm'
          variant='outline'
          aria-label={t('Copy engineer workspace link')}
        >
          {t('Copy engineer workspace link')}
        </CopyButton>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Add real people and bind their existing user accounts. No sample engineers are published.'
          )}
        </p>
        {canWrite && (
          <Button onClick={() => setEditing({ ...blankEngineer })}>
            {t('Add engineer')}
          </Button>
        )}
      </div>
      <StaticDataTable
        data={query.data}
        getRowKey={(row) => row.id}
        emptyContent={t('No engineers have been added')}
        columns={[
          {
            id: 'name',
            header: t('Engineer'),
            cell: (row) => (
              <div>
                <span className='font-medium'>{row.display_name}</span>
                <p className='text-muted-foreground text-xs'>
                  {t('User ID')}: {row.user_id}
                </p>
              </div>
            ),
          },
          {
            id: 'state',
            header: t('Availability'),
            cell: (row) => t(engineerAvailabilityLabel(row)),
          },
          {
            id: 'capacity',
            header: t('Open demands'),
            cell: (row) =>
              `${formatNumber(row.open_demand_count)} / ${formatNumber(row.capacity)}`,
          },
          {
            id: 'hours',
            header: t('Service hours'),
            cell: (row) => row.service_hours || t('Not set'),
          },
          {
            id: 'edit',
            header: t('Actions'),
            cell: (row) =>
              canWrite && (
                <Button variant='outline' onClick={() => setEditing(row)}>
                  {t('Edit')}
                </Button>
              ),
          },
        ]}
      />
      {editing && (
        <Dialog
          open
          onOpenChange={(open) => {
            if (!open && !save.isPending) setEditing(null)
          }}
          title={t(editing.id ? 'Edit engineer' : 'Add engineer')}
        >
          <EngineerForm
            initial={editing}
            config={config.data}
            pending={save.isPending}
            onSave={(value) => save.mutate(value)}
          />
          {save.isError && (
            <p role='alert' className='text-destructive mt-3 text-sm'>
              {t(
                'The profile was not saved. Review the error and retry; your edits are retained.'
              )}
            </p>
          )}
        </Dialog>
      )}
    </section>
  )
}

function EngineerForm({
  initial,
  config,
  pending,
  onSave,
}: {
  initial: Engineer
  config: DemandConfig
  pending: boolean
  onSave: (v: Engineer) => void
}) {
  const { t } = useTranslation()
  const form = useForm<z.infer<typeof formSchema>>({
    defaultValues: { ...initial, user_id: initial.user_id ?? 0 },
    resolver: zodResolver(formSchema),
  })
  const [avatar, setAvatar] = useState<string | null>(null)
  const upload = useMutation({
    retry: false,
    mutationFn: async (file: File) => {
      const result = await uploadDemandFile(
        file,
        crypto.randomUUID(),
        () => undefined,
        'admin'
      )
      if (result.kind !== 'image') throw new Error('An image is required')
      return result
    },
    onSuccess: (result) => {
      form.setValue('avatar_attachment_id', result.id, { shouldDirty: true })
      void attachmentLink(result.id, 'inline', 'admin')
        .then((link) => setAvatar(link.url))
        .catch(() => setAvatar(null))
    },
  })
  return (
    <Form {...form}>
      <form className='space-y-4' onSubmit={form.handleSubmit(onSave)}>
        <div className='grid gap-4 sm:grid-cols-2'>
          <FormField
            control={form.control}
            name='user_id'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('User ID')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={1}
                    disabled={!!initial.id || pending}
                    {...field}
                    onChange={(e) => field.onChange(e.target.valueAsNumber)}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='display_name'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Display name')}</FormLabel>
                <FormControl>
                  <Input {...field} maxLength={100} disabled={pending} />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='capacity'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Concurrent demand capacity')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={1}
                    max={1000}
                    {...field}
                    onChange={(e) => field.onChange(e.target.valueAsNumber)}
                    disabled={pending}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='sort'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Sort order')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    {...field}
                    onChange={(e) => field.onChange(e.target.valueAsNumber)}
                    disabled={pending}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
        </div>
        <FormField
          control={form.control}
          name='category_ids'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Specialties')}</FormLabel>
              <div className='flex flex-wrap gap-4'>
                {config.config.categories.map((category) => (
                  <label
                    key={category.id}
                    className='flex min-h-11 items-center gap-2 text-sm'
                  >
                    <Checkbox
                      checked={field.value.includes(category.id)}
                      disabled={pending}
                      onCheckedChange={(checked) =>
                        field.onChange(
                          checked
                            ? [...field.value, category.id]
                            : field.value.filter((id) => id !== category.id)
                        )
                      }
                    />
                    {category.name}
                  </label>
                ))}
              </div>
              <FormMessage />
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name='introduction'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Introduction')}</FormLabel>
              <FormControl>
                <Textarea {...field} maxLength={2000} disabled={pending} />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />
        <FormField
          control={form.control}
          name='service_hours'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Service hours')}</FormLabel>
              <FormControl>
                <Input {...field} maxLength={300} disabled={pending} />
              </FormControl>
              <FormMessage />
            </FormItem>
          )}
        />
        <div className='space-y-2'>
          <label className='text-sm font-medium' htmlFor='engineer-avatar'>
            {t('Engineer avatar')}
          </label>
          <Input
            id='engineer-avatar'
            type='file'
            accept='image/jpeg,image/png,image/webp'
            disabled={pending || upload.isPending}
            onChange={(e) => {
              const file = e.target.files?.[0]
              if (file) upload.mutate(file)
              e.target.value = ''
            }}
          />
          {avatar && (
            <img
              src={avatar}
              alt={t('Engineer avatar')}
              className='size-16 rounded-full object-cover'
            />
          )}
          {upload.isPending && (
            <p role='status' className='text-sm'>
              {t('Uploading avatar...')}
            </p>
          )}
          {upload.isError && (
            <p role='alert' className='text-destructive text-sm'>
              {t('Avatar upload failed; profile has not changed')}
            </p>
          )}
        </div>
        <div className='flex flex-wrap gap-5'>
          {(['enabled', 'published', 'accepting'] as const).map((name) => (
            <FormField
              key={name}
              control={form.control}
              name={name}
              render={({ field }) => (
                <FormItem className='flex min-h-11 items-center gap-2'>
                  <FormControl>
                    <Checkbox
                      checked={field.value}
                      disabled={pending}
                      onCheckedChange={field.onChange}
                    />
                  </FormControl>
                  <FormLabel>
                    {t(
                      {
                        enabled: 'Engineer access enabled',
                        published: 'Listed in directory',
                        accepting: 'Accepting demands',
                      }[name]
                    )}
                  </FormLabel>
                </FormItem>
              )}
            />
          ))}
        </div>
        <Button type='submit' disabled={pending || upload.isPending}>
          {t(pending ? 'Saving...' : 'Save engineer')}
        </Button>
      </form>
    </Form>
  )
}

function engineerAvailabilityLabel(engineer: Engineer) {
  if (!engineer.enabled) return 'Disabled'
  if (!engineer.published) return 'Unpublished'
  return { online: 'Online', busy: 'Busy', offline: 'Offline' }[
    engineer.presence
  ]
}
