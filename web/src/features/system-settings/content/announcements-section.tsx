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
import { Plus, Trash2, Save } from 'lucide-react'
import { useEffect, useMemo, useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { StaticDataTable } from '@/components/data-table/static/static-data-table'
import { StaticRowActions } from '@/components/data-table/static/static-row-actions'
import { DateTimePicker } from '@/components/datetime-picker'
import { Dialog } from '@/components/dialog'
import { StatusBadge } from '@/components/status-badge'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
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
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { toIntlLocale } from '@/i18n/languages'
import { formatTimestampRelative } from '@/lib/format'
import { handleServerError } from '@/lib/handle-server-error'
import { formatDateTimeObject } from '@/lib/time'

import { SettingsSwitchField } from '../components/settings-form-layout'
import { SettingsSection } from '../components/settings-section'
import { useUpdateOption } from '../hooks/use-update-option'
import {
  announcementSchema,
  announcementTitle,
  MAX_ANNOUNCEMENTS,
  parseAnnouncements,
  serializeAnnouncements,
  type Announcement,
  type AnnouncementFormValues,
} from './announcements'

type AnnouncementsSectionProps = {
  enabled: boolean
  data: string
  readOnly?: boolean
  saveSettings?: (
    data: string,
    enabled: boolean
  ) => Promise<{ data: string; enabled: boolean }>
}

const ANNOUNCEMENT_FORM_ID = 'announcement-form'

const typeOptions = [
  {
    value: 'default',
    label: 'Default',
    color: 'bg-gray-500',
    badgeVariant: 'neutral' as const,
  },
  {
    value: 'ongoing',
    label: 'Ongoing',
    color: 'bg-blue-500',
    badgeVariant: 'info' as const,
  },
  {
    value: 'success',
    label: 'Success',
    color: 'bg-green-500',
    badgeVariant: 'success' as const,
  },
  {
    value: 'warning',
    label: 'Warning',
    color: 'bg-orange-500',
    badgeVariant: 'warning' as const,
  },
  {
    value: 'error',
    label: 'Error',
    color: 'bg-red-500',
    badgeVariant: 'danger' as const,
  },
]

export function AnnouncementsSection({
  enabled,
  data,
  readOnly = false,
  saveSettings,
}: AnnouncementsSectionProps) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const updateOption = useUpdateOption()
  const [announcements, setAnnouncements] = useState<Announcement[]>([])
  const [isEnabled, setIsEnabled] = useState(enabled)
  const [hasChanges, setHasChanges] = useState(false)
  const [saving, setSaving] = useState(false)
  const busy = saving || updateOption.isPending
  const [parseError, setParseError] = useState(false)
  const loadedDataRef = useRef<string | null>(null)
  const [selectedIds, setSelectedIds] = useState<number[]>([])
  const [showDialog, setShowDialog] = useState(false)
  const [showDeleteDialog, setShowDeleteDialog] = useState(false)
  const [editingAnnouncement, setEditingAnnouncement] =
    useState<Announcement | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<'single' | 'batch'>('single')

  const form = useForm<AnnouncementFormValues>({
    resolver: zodResolver(announcementSchema),
    defaultValues: {
      title: '',
      content: '',
      publishDate: new Date().toISOString(),
      type: 'default',
      extra: '',
    },
  })

  useEffect(() => {
    if (hasChanges || loadedDataRef.current === data) return
    loadedDataRef.current = data
    try {
      setAnnouncements(parseAnnouncements(data))
      setSelectedIds([])
      setParseError(false)
    } catch {
      setAnnouncements([])
      setParseError(true)
    }
  }, [data, hasChanges])

  useEffect(() => {
    if (!hasChanges) setIsEnabled(enabled)
  }, [enabled, hasChanges])

  const handleToggleEnabled = async (checked: boolean) => {
    if (readOnly || busy) return
    if (saveSettings) {
      setIsEnabled(checked)
      setHasChanges(true)
      return
    }
    try {
      await updateOption.mutateAsync({
        key: 'console_setting.announcements_enabled',
        value: checked,
      })
      setIsEnabled(checked)
      toast.success(t('Setting saved'))
    } catch (error) {
      handleServerError(error, t('Failed to update setting'))
    }
  }

  const handleAdd = () => {
    if (readOnly || busy) return
    setEditingAnnouncement(null)
    form.reset({
      title: '',
      content: '',
      publishDate: new Date().toISOString(),
      type: 'default',
      extra: '',
    })
    setShowDialog(true)
  }

  const handleEdit = (announcement: Announcement) => {
    if (readOnly || busy) return
    setEditingAnnouncement(announcement)
    form.reset({
      title: announcement.title || '',
      content: announcement.content,
      publishDate: announcement.publishDate,
      type: announcement.type,
      extra: announcement.extra || '',
    })
    setShowDialog(true)
  }

  const handleDelete = (announcement: Announcement) => {
    if (readOnly || busy) return
    setEditingAnnouncement(announcement)
    setDeleteTarget('single')
    setShowDeleteDialog(true)
  }

  const handleBatchDelete = () => {
    if (readOnly || busy) return
    if (selectedIds.length === 0) {
      toast.error(t('Please select items to delete'))
      return
    }
    setDeleteTarget('batch')
    setShowDeleteDialog(true)
  }

  const confirmDelete = () => {
    if (readOnly || busy) return
    if (deleteTarget === 'single' && editingAnnouncement) {
      setAnnouncements((prev) =>
        prev.filter((item) => item.id !== editingAnnouncement.id)
      )
      setHasChanges(true)
      toast.success(t('Announcement deleted. Click "Save Settings" to apply.'))
    } else if (deleteTarget === 'batch') {
      setAnnouncements((prev) =>
        prev.filter((item) => !selectedIds.includes(item.id))
      )
      setSelectedIds([])
      setHasChanges(true)
      toast.success(
        t('{{count}} announcements deleted. Click "Save Settings" to apply.', {
          count: selectedIds.length,
        })
      )
    }
    setShowDeleteDialog(false)
    setEditingAnnouncement(null)
  }

  const handleSubmitForm = (values: AnnouncementFormValues) => {
    if (readOnly || busy) return
    if (!editingAnnouncement && announcements.length >= MAX_ANNOUNCEMENTS) {
      toast.error(t('Maximum 100 announcements'))
      return
    }
    if (editingAnnouncement) {
      setAnnouncements((prev) =>
        prev.map((item) =>
          item.id === editingAnnouncement.id ? { ...item, ...values } : item
        )
      )
      toast.success(t('Announcement updated. Click "Save Settings" to apply.'))
    } else {
      const usedIds = new Set(announcements.map((item) => item.id))
      let newId = 1
      while (usedIds.has(newId)) newId++
      setAnnouncements((prev) => [...prev, { id: newId, ...values }])
      toast.success(t('Announcement added. Click "Save Settings" to apply.'))
    }
    setHasChanges(true)
    setShowDialog(false)
  }

  const handleSaveAll = async () => {
    if (
      readOnly ||
      busy ||
      parseError ||
      announcements.length > MAX_ANNOUNCEMENTS
    ) {
      return
    }
    for (const announcement of announcements) {
      const result = announcementSchema.safeParse(announcement)
      if (!result.success) {
        toast.error(t(result.error.issues[0].message))
        return
      }
    }
    setSaving(true)
    try {
      if (saveSettings) {
        const saved = await saveSettings(
          serializeAnnouncements(announcements),
          isEnabled
        )
        loadedDataRef.current = saved.data
        setAnnouncements(parseAnnouncements(saved.data))
        setIsEnabled(saved.enabled)
      } else {
        await updateOption.mutateAsync({
          key: 'console_setting.announcements',
          value: serializeAnnouncements(announcements),
        })
      }
      setHasChanges(false)
      toast.success(t('Announcements saved successfully'))
    } catch (error) {
      handleServerError(error, t('Failed to save announcements'))
    } finally {
      setSaving(false)
    }
  }

  const toggleSelectAll = (checked: boolean) => {
    setSelectedIds(checked ? announcements.map((item) => item.id) : [])
  }

  const toggleSelectOne = (id: number, checked: boolean) => {
    setSelectedIds((prev) =>
      checked ? [...prev, id] : prev.filter((item) => item !== id)
    )
  }

  const sortedAnnouncements = useMemo(() => {
    return [...announcements].sort((a, b) => {
      return (
        new Date(b.publishDate).getTime() - new Date(a.publishDate).getTime()
      )
    })
  }, [announcements])

  return (
    <SettingsSection title={t('Announcements')}>
      {readOnly && (
        <p className='text-muted-foreground text-sm'>
          {t(
            'Read-only access. Contact a super administrator to change these settings.'
          )}
        </p>
      )}
      <div className='space-y-4'>
        <div className='flex flex-wrap items-center justify-between gap-2'>
          <div className='flex flex-wrap items-center gap-2'>
            <Button
              onClick={handleAdd}
              size='sm'
              disabled={
                readOnly ||
                parseError ||
                busy ||
                announcements.length >= MAX_ANNOUNCEMENTS
              }
            >
              <Plus className='mr-2 h-4 w-4' />
              {t('Add Announcement')}
            </Button>
            <Button
              onClick={handleBatchDelete}
              size='sm'
              variant='destructive'
              disabled={readOnly || selectedIds.length === 0 || busy}
            >
              <Trash2 className='mr-2 h-4 w-4' />
              {t('Delete (')}
              {selectedIds.length})
            </Button>
            <Button
              onClick={handleSaveAll}
              size='sm'
              variant='secondary'
              disabled={
                readOnly ||
                !hasChanges ||
                busy ||
                parseError ||
                announcements.length > MAX_ANNOUNCEMENTS
              }
            >
              <Save className='mr-2 h-4 w-4' />
              {busy ? t('Saving...') : t('Save Settings')}
            </Button>
          </div>
          <SettingsSwitchField
            controlId='announcements-enabled'
            disabled={readOnly || busy}
            checked={isEnabled}
            onCheckedChange={handleToggleEnabled}
            label={t('Enabled')}
            className='py-0'
          />
        </div>

        {parseError && (
          <p role='alert' className='text-destructive text-sm'>
            {t(
              'Unable to read announcements. Fix the stored JSON before saving.'
            )}
          </p>
        )}
        {announcements.length >= MAX_ANNOUNCEMENTS && (
          <p
            role={
              announcements.length > MAX_ANNOUNCEMENTS ? 'alert' : undefined
            }
            className='text-muted-foreground text-sm'
          >
            {t('Maximum 100 announcements')}
          </p>
        )}
        <StaticDataTable
          data={sortedAnnouncements}
          getRowKey={(announcement) => announcement.id}
          emptyContent={t(
            'No announcements yet. Click "Add Announcement" to create one.'
          )}
          columns={[
            {
              id: 'select',
              header: (
                <Checkbox
                  aria-label={t('Select all announcements')}
                  disabled={readOnly || busy}
                  checked={
                    selectedIds.length === announcements.length &&
                    announcements.length > 0
                  }
                  onCheckedChange={toggleSelectAll}
                />
              ),
              className: 'w-12',
              cell: (announcement) => (
                <Checkbox
                  aria-label={t('Select announcement {{title}}', {
                    title: announcementTitle(announcement),
                  })}
                  checked={selectedIds.includes(announcement.id)}
                  disabled={readOnly || busy}
                  onCheckedChange={(checked) =>
                    toggleSelectOne(announcement.id, checked as boolean)
                  }
                />
              ),
            },
            {
              id: 'title',
              header: t('Title'),
              cellClassName: 'max-w-xs',
              cell: (announcement) => (
                <div className='max-w-xs min-w-0'>
                  <p
                    className='truncate font-medium'
                    title={announcementTitle(announcement)}
                  >
                    {announcementTitle(announcement)}
                  </p>
                  <p
                    className='text-muted-foreground truncate text-xs'
                    title={announcement.content}
                  >
                    {announcement.content}
                  </p>
                </div>
              ),
            },
            {
              id: 'publish-date',
              header: t('Publish Date'),
              cell: (announcement) =>
                Number.isFinite(Date.parse(announcement.publishDate)) ? (
                  <div className='flex flex-col gap-1'>
                    <span className='text-sm font-medium'>
                      {formatTimestampRelative(
                        Date.parse(announcement.publishDate),
                        'milliseconds',
                        locale
                      )}
                    </span>
                    <time
                      dateTime={announcement.publishDate}
                      className='text-muted-foreground text-xs'
                    >
                      {formatDateTimeObject(new Date(announcement.publishDate))}
                    </time>
                  </div>
                ) : (
                  t('Unknown')
                ),
            },
            {
              id: 'updated-at',
              header: t('Server update time'),
              cell: (announcement) =>
                announcement.updatedAt &&
                Number.isFinite(Date.parse(announcement.updatedAt)) ? (
                  <time
                    dateTime={announcement.updatedAt}
                    className='text-muted-foreground text-xs'
                  >
                    {formatDateTimeObject(new Date(announcement.updatedAt))}
                  </time>
                ) : (
                  t('Unknown')
                ),
            },
            {
              id: 'type',
              header: t('Type'),
              cell: (announcement) => (
                <StatusBadge
                  label={t(
                    typeOptions.find((opt) => opt.value === announcement.type)
                      ?.label || 'Default'
                  )}
                  variant={
                    typeOptions.find((opt) => opt.value === announcement.type)
                      ?.badgeVariant ?? 'neutral'
                  }
                  copyable={false}
                />
              ),
            },
            {
              id: 'extra',
              header: t('Extra'),
              cellClassName: 'text-muted-foreground max-w-xs truncate',
              cell: (announcement) => announcement.extra || '-',
            },
            {
              id: 'actions',
              header: t('Actions'),
              cell: (announcement) => (
                <StaticRowActions
                  editLabel={t('Edit')}
                  deleteLabel={t('Delete')}
                  menuLabel={t('Open menu')}
                  editDisabled={readOnly || busy}
                  deleteDisabled={readOnly || busy}
                  onEdit={() => handleEdit(announcement)}
                  onDelete={() => handleDelete(announcement)}
                />
              ),
            },
          ]}
        />
      </div>

      <Dialog
        open={showDialog && !readOnly}
        onOpenChange={setShowDialog}
        title={
          editingAnnouncement ? t('Edit Announcement') : t('Add Announcement')
        }
        description={t(
          'Create or update system announcements for the dashboard'
        )}
        contentClassName='max-w-2xl'
        contentHeight='auto'
        bodyClassName='space-y-4'
        footer={
          <>
            <Button
              type='button'
              variant='outline'
              onClick={() => setShowDialog(false)}
            >
              {t('Cancel')}
            </Button>
            <Button
              type='submit'
              form={ANNOUNCEMENT_FORM_ID}
              disabled={readOnly || busy}
            >
              {editingAnnouncement ? t('Update') : t('Add')}
            </Button>
          </>
        }
      >
        <Form {...form}>
          <form
            id={ANNOUNCEMENT_FORM_ID}
            onSubmit={form.handleSubmit(handleSubmitForm)}
            className='space-y-4'
          >
            <FormField
              control={form.control}
              name='title'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Announcement title (optional)')}</FormLabel>
                  <FormControl>
                    <Input
                      placeholder={t('Enter announcement title')}
                      maxLength={120}
                      {...field}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Maximum 120 characters. Leave blank to use a content preview.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
            <div className='rounded-lg border p-3 text-sm'>
              <p className='font-medium'>{t('Server update time')}</p>
              <p className='text-muted-foreground mt-1'>
                {editingAnnouncement?.updatedAt &&
                Number.isFinite(Date.parse(editingAnnouncement.updatedAt))
                  ? formatDateTimeObject(
                      new Date(editingAnnouncement.updatedAt)
                    )
                  : t('Unknown')}
              </p>
              <p className='text-muted-foreground mt-1 text-xs'>
                {t(
                  'Set by the server after saving. Older announcements may have no recorded update time.'
                )}
              </p>
            </div>
            <FormField
              control={form.control}
              name='content'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Content')}</FormLabel>
                  <FormControl>
                    <Textarea
                      placeholder={t(
                        'Enter announcement content (supports Markdown/HTML)'
                      )}
                      rows={4}
                      {...field}
                    />
                  </FormControl>
                  <FormDescription>
                    {t('Maximum 500 characters. Supports Markdown and HTML.')}{' '}
                    {t(
                      'Plain text for desktop clients; the dashboard keeps its existing rich content display.'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='publishDate'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Publish Date')}</FormLabel>
                  <FormControl>
                    <DateTimePicker
                      value={
                        Number.isFinite(Date.parse(field.value))
                          ? new Date(field.value)
                          : undefined
                      }
                      onChange={(date) =>
                        field.onChange(date ? date.toISOString() : '')
                      }
                      placeholder={t('Select publish date')}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Date and time when this announcement should be displayed'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='type'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Type')}</FormLabel>
                  <Select
                    items={typeOptions.map((option) => ({
                      value: option.value,
                      label: (
                        <div className='flex items-center gap-2'>
                          <div
                            className={`h-3 w-3 rounded-full ${option.color}`}
                          />
                          {t(option.label)}
                        </div>
                      ),
                    }))}
                    onValueChange={field.onChange}
                    value={field.value}
                  >
                    <FormControl>
                      <SelectTrigger>
                        <SelectValue
                          placeholder={t('Select announcement type')}
                        />
                      </SelectTrigger>
                    </FormControl>
                    <SelectContent alignItemWithTrigger={false}>
                      <SelectGroup>
                        {typeOptions.map((option) => (
                          <SelectItem key={option.value} value={option.value}>
                            <div className='flex items-center gap-2'>
                              <div
                                className={`h-3 w-3 rounded-full ${option.color}`}
                              />
                              {t(option.label)}
                            </div>
                          </SelectItem>
                        ))}
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                  <FormMessage />
                </FormItem>
              )}
            />
            <FormField
              control={form.control}
              name='extra'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Extra Notes (Optional)')}</FormLabel>
                  <FormControl>
                    <Input
                      placeholder={t('Additional information')}
                      {...field}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Optional supplementary information (max 100 characters)'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </form>
        </Form>
      </Dialog>

      <ConfirmDialog
        open={showDeleteDialog && !readOnly}
        onOpenChange={setShowDeleteDialog}
        title={t('Are you sure?')}
        desc={
          deleteTarget === 'single'
            ? t('This announcement will be removed from the list.')
            : t('{{count}} announcements will be removed from the list.', {
                count: selectedIds.length,
              })
        }
        confirmText={t('Delete')}
        destructive
        handleConfirm={confirmDelete}
      />
    </SettingsSection>
  )
}
