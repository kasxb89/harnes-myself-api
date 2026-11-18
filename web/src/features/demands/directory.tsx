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
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { ConfirmDialog } from '@/components/confirm-dialog'
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

import { attachmentLink, loadDemandConfig, uploadDemandFile } from './api'
import {
  importDirectoryPreset,
  loadDirectory,
  newDirectoryProfile,
  profileSchema,
  saveDirectoryProfile,
  type DirectoryKind,
  type DirectoryProfile,
} from './directory-api'

function ProfileAvatar(props: { id: string; accountId: number }) {
  const { t } = useTranslation()
  const query = useQuery({
    queryKey: ['demand-profile-avatar', props.accountId, props.id],
    enabled: !!props.id,
    queryFn: () => attachmentLink(props.id, 'inline', 'admin'),
    staleTime: 45000,
    retry: false,
  })
  if (!query.data) {
    return (
      <span
        aria-hidden
        className='bg-muted block size-10 shrink-0 rounded-lg'
      />
    )
  }
  return (
    <img
      src={query.data.url}
      alt={t('Profile avatar')}
      className='size-10 shrink-0 rounded-lg object-cover'
      onError={() => {
        void query.refetch()
      }}
    />
  )
}
export function DemandDirectory(props: {
  accountId: number
  canWrite: boolean
  kind: DirectoryKind
  onViewDemands: (id: string) => void
}) {
  const { t } = useTranslation()
  const cache = useQueryClient()
  const [search, setSearch] = useState('')
  const [pages, setPages] = useState<string[]>([''])
  const [editing, setEditing] = useState<DirectoryProfile | null>(null)
  const [confirmImport, setConfirmImport] = useState(false)
  const service = props.kind === 'service-profiles'
  const cursor = pages.at(-1) ?? ''
  const query = useQuery({
    queryKey: ['demand-directory', props.accountId, props.kind, search, cursor],
    queryFn: ({ signal }) => loadDirectory(props.kind, search, cursor, signal),
    retry: false,
  })
  const save = useMutation({
    mutationFn: (profile: DirectoryProfile) =>
      saveDirectoryProfile(props.kind, profile),
    retry: false,
    onSuccess: () => {
      setEditing(null)
      void cache.invalidateQueries({
        queryKey: ['demand-directory', props.accountId],
      })
    },
  })
  const preset = useMutation({
    mutationFn: importDirectoryPreset,
    retry: false,
    onSuccess: () => {
      setConfirmImport(false)
      void cache.invalidateQueries({
        queryKey: ['demand-directory', props.accountId],
      })
    },
  })
  return (
    <section
      className='space-y-4'
      aria-label={t(service ? 'Service profiles' : 'Customer profiles')}
    >
      <p className='text-muted-foreground max-w-3xl text-sm'>
        {t(
          service
            ? 'Service profiles are platform intake entries. All requests enter the central queue before a real engineer is assigned.'
            : 'Customer profiles are internal records. Linking an existing account associates its demands without creating a login or changing its permissions.'
        )}
      </p>
      <div className='flex flex-wrap items-center gap-3'>
        <Input
          aria-label={t('Search profiles')}
          placeholder={t('Search name, code or direction')}
          value={search}
          className='w-full sm:max-w-sm'
          onChange={(e) => {
            setSearch(e.target.value)
            setPages([''])
          }}
          maxLength={100}
        />
        <span className='text-muted-foreground text-sm' role='status'>
          {t('Matching profiles')}: {formatNumber(query.data?.total ?? 0)}
        </span>
        {props.canWrite && (
          <>
            <Button
              variant='outline'
              onClick={() => setConfirmImport(true)}
              disabled={preset.isPending}
            >
              {t('Import 100 service and 100 customer profiles')}
            </Button>
            <Button onClick={() => setEditing({ ...newDirectoryProfile })}>
              {t(service ? 'Add service profile' : 'Add customer profile')}
            </Button>
          </>
        )}
      </div>
      {preset.data && (
        <p role='status' className='text-sm'>
          {t(
            preset.data.replayed
              ? 'This preset batch was already imported; edits were retained.'
              : 'Import completed; existing records were retained.'
          )}
        </p>
      )}
      {query.isPending && <LoadingState />}
      {query.isError && (
        <ErrorState
          onRetry={() => {
            void query.refetch()
          }}
        />
      )}
      {query.data && (
        <StaticDataTable
          data={query.data.items}
          getRowKey={(row) => row.id}
          emptyContent={t('No matching profiles')}
          columns={[
            {
              id: 'name',
              header: t('Profile'),
              cell: (row) => (
                <div className='flex items-center gap-3'>
                  <ProfileAvatar
                    id={row.avatar_attachment_id}
                    accountId={props.accountId}
                  />
                  <div className='min-w-0'>
                    <p className='font-medium break-words'>
                      {row.display_name}
                    </p>
                    <p className='text-muted-foreground text-xs'>{row.code}</p>
                  </div>
                </div>
              ),
            },
            {
              id: 'direction',
              header: t(service ? 'Service direction' : 'Organization'),
              cell: (row) => (
                <p className='max-w-sm break-words whitespace-normal'>
                  {service
                    ? row.service_direction
                    : row.organization || t('Not set')}
                </p>
              ),
            },
            {
              id: 'status',
              header: t('Status'),
              cell: (row) => (
                <span>
                  {t(row.enabled ? 'Enabled' : 'Disabled')}
                  {service &&
                    ` · ${t(row.published ? 'Listed in directory' : 'Unpublished')}`}
                  {!service &&
                    ` · ${row.user_id ? t('Linked account') : t('Unbound')}`}
                </span>
              ),
            },
            {
              id: 'actions',
              header: t('Actions'),
              cell: (row) => (
                <div className='flex flex-wrap gap-2'>
                  {props.canWrite && (
                    <Button variant='outline' onClick={() => setEditing(row)}>
                      {t('Edit')}
                    </Button>
                  )}
                  {!service && row.user_id > 0 && (
                    <Button
                      variant='ghost'
                      onClick={() => props.onViewDemands(row.id)}
                    >
                      {t('View demands')}
                    </Button>
                  )}
                </div>
              ),
            },
          ]}
        />
      )}
      <div className='flex gap-2'>
        <Button
          variant='outline'
          disabled={pages.length === 1 || query.isFetching}
          onClick={() => setPages((old) => old.slice(0, -1))}
        >
          {t('Previous page')}
        </Button>
        <Button
          variant='outline'
          disabled={!query.data?.has_more || query.isFetching}
          onClick={() =>
            setPages((old) => [...old, query.data?.next_after_id ?? ''])
          }
        >
          {t('Next page')}
        </Button>
      </div>
      <ConfirmDialog
        open={confirmImport}
        onOpenChange={setConfirmImport}
        title={t('Import platform directory')}
        desc={t(
          'Create missing service and internal customer profiles with generic role avatars. No user accounts are created. Reimporting keeps administrator edits.'
        )}
        isLoading={preset.isPending}
        handleConfirm={() => preset.mutate()}
      />
      {editing && (
        <Dialog
          open
          title={t(service ? 'Edit service profile' : 'Edit customer profile')}
          onOpenChange={(open) => {
            if (!open && !save.isPending) setEditing(null)
          }}
        >
          <DirectoryForm
            key={editing.id || 'new'}
            initial={editing}
            service={service}
            accountId={props.accountId}
            pending={save.isPending}
            onSave={(profile) => save.mutate(profile)}
          />
          {save.isError && (
            <p role='alert' className='text-destructive mt-3'>
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
function DirectoryForm(props: {
  initial: DirectoryProfile
  service: boolean
  accountId: number
  pending: boolean
  onSave: (profile: DirectoryProfile) => void
}) {
  const { t } = useTranslation()
  const form = useForm<DirectoryProfile>({
    defaultValues: props.initial,
    resolver: zodResolver(
      profileSchema
        .extend({
          service_direction:
            profileSchema.shape.service_direction.removeDefault(),
          category_ids: profileSchema.shape.category_ids.removeDefault(),
          published: profileSchema.shape.published.removeDefault(),
          sort: profileSchema.shape.sort.removeDefault(),
          organization: profileSchema.shape.organization.removeDefault(),
          notes: profileSchema.shape.notes.removeDefault(),
          user_id: profileSchema.shape.user_id.removeDefault(),
        })
        .refine((v) => !props.service || !!v.service_direction.trim(), {
          path: ['service_direction'],
          message: t('Service direction is required'),
        })
    ),
  })
  const config = useQuery({
    queryKey: ['demand-config', props.accountId],
    queryFn: ({ signal }) => loadDemandConfig(signal),
    retry: false,
    enabled: props.service,
  })
  const uploadAbort = useRef<AbortController | null>(null)
  useEffect(() => () => uploadAbort.current?.abort(), [])
  const upload = useMutation({
    retry: false,
    mutationFn: async (file: File) => {
      uploadAbort.current?.abort()
      const controller = new AbortController()
      uploadAbort.current = controller
      const value = await uploadDemandFile(
        file,
        crypto.randomUUID(),
        () => undefined,
        'admin',
        controller.signal
      )
      if (value.kind !== 'image') throw new Error('An image is required')
      return value
    },
    onSuccess: (value) => {
      form.setValue('avatar_attachment_id', value.id, { shouldDirty: true })
    },
  })
  const disabled = props.pending || upload.isPending
  const strings: (
    | 'code'
    | 'display_name'
    | 'service_direction'
    | 'organization'
    | 'introduction'
    | 'notes'
  )[] = props.service
    ? ['code', 'display_name', 'service_direction', 'introduction']
    : ['code', 'display_name', 'organization', 'introduction', 'notes']
  const labels = {
    code: 'Profile code',
    display_name: 'Display name',
    service_direction: 'Service direction',
    organization: 'Organization',
    introduction: 'Introduction',
    notes: 'Internal notes',
  }
  return (
    <Form {...form}>
      <form onSubmit={form.handleSubmit(props.onSave)} className='space-y-4'>
        {strings.map((name) => (
          <FormField
            key={name}
            name={name}
            control={form.control}
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t(labels[name])}</FormLabel>
                <FormControl>
                  {name === 'introduction' || name === 'notes' ? (
                    <Textarea
                      {...field}
                      maxLength={name === 'notes' ? 4000 : 2000}
                      disabled={disabled}
                    />
                  ) : (
                    <Input
                      {...field}
                      maxLength={
                        {
                          code: 80,
                          display_name: 100,
                          service_direction: 200,
                          organization: 200,
                          introduction: 2000,
                          notes: 4000,
                        }[name]
                      }
                      disabled={
                        disabled || (name === 'code' && !!props.initial.id)
                      }
                    />
                  )}
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
        ))}
        {props.service && (
          <FormField
            name='category_ids'
            control={form.control}
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Specialties')}</FormLabel>
                {config.isError && (
                  <p role='alert'>
                    {t(
                      'Categories could not be loaded. Existing selections are retained.'
                    )}
                  </p>
                )}
                <div className='flex flex-wrap gap-4'>
                  {config.data?.config.categories.map((category) => (
                    <label
                      key={category.id}
                      className='flex min-h-11 items-center gap-2 text-sm'
                    >
                      <Checkbox
                        checked={field.value.includes(category.id)}
                        disabled={disabled}
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
        )}
        {!props.service && (
          <FormField
            name='user_id'
            control={form.control}
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Linked user ID (0 means unbound)')}</FormLabel>
                <FormControl>
                  <Input
                    type='number'
                    min={0}
                    {...field}
                    onChange={(e) => field.onChange(e.target.valueAsNumber)}
                    disabled={disabled}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
        )}
        <div className='space-y-2'>
          <label htmlFor='profile-avatar' className='text-sm font-medium'>
            {t('Profile avatar')}
          </label>
          <div className='flex items-center gap-3'>
            <ProfileAvatar
              id={form.watch('avatar_attachment_id')}
              accountId={props.accountId}
            />
            <Input
              id='profile-avatar'
              type='file'
              accept='image/png,image/jpeg,image/webp'
              disabled={disabled}
              onChange={(e) => {
                const file = e.target.files?.[0]
                if (file) upload.mutate(file)
                e.target.value = ''
              }}
            />
            <Button
              type='button'
              variant='ghost'
              disabled={disabled}
              onClick={() =>
                form.setValue('avatar_attachment_id', '', { shouldDirty: true })
              }
            >
              {t('Remove')}
            </Button>
          </div>
          {upload.isPending && <p role='status'>{t('Uploading avatar...')}</p>}
          {upload.isError && (
            <p role='alert' className='text-destructive'>
              {t('Avatar upload failed; profile has not changed')}
            </p>
          )}
        </div>
        <div className='flex flex-wrap gap-5'>
          {(props.service
            ? (['enabled', 'published'] as const)
            : (['enabled'] as const)
          ).map((name) => (
            <FormField
              key={name}
              name={name}
              control={form.control}
              render={({ field }) => (
                <FormItem className='flex min-h-11 items-center gap-2'>
                  <FormControl>
                    <Checkbox
                      checked={field.value}
                      onCheckedChange={field.onChange}
                      disabled={disabled}
                    />
                  </FormControl>
                  <FormLabel>
                    {t(
                      name === 'published' ? 'Listed in directory' : 'Enabled'
                    )}
                  </FormLabel>
                </FormItem>
              )}
            />
          ))}
        </div>
        <Button type='submit' disabled={disabled}>
          {t(props.pending ? 'Saving...' : 'Save profile')}
        </Button>
      </form>
    </Form>
  )
}
