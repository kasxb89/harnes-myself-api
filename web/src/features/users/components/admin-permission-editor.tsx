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

import { EmptyState } from '@/components/empty-state'
import {
  PermissionMatrix,
  type PermissionMatrixGroup,
} from '@/components/permission-matrix'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import type {
  AdminPermissionMatrix,
  PermissionCatalog,
} from '@/lib/admin-permissions'
import { SIDEBAR_MODULE_SECTIONS } from '@/lib/sidebar-modules'

type AdminPermissionEditorProps = {
  catalog: PermissionCatalog
  value: AdminPermissionMatrix
  initialValue: AdminPermissionMatrix
  onChange: (value: AdminPermissionMatrix) => void
  disabled?: boolean
}

export function AdminPermissionEditor(props: AdminPermissionEditorProps) {
  const { t } = useTranslation()
  const [search, setSearch] = useState('')
  const groups = new Map<string, PermissionMatrixGroup>()
  let selected = 0
  let added = 0
  let removed = 0
  const query = search.trim().toLocaleLowerCase()
  for (const resource of props.catalog.resources) {
    for (const action of resource.actions) {
      if (action.root_only) continue
      const current = props.value[resource.resource]?.[action.action] === true
      const original =
        props.initialValue[resource.resource]?.[action.action] === true
      if (current) selected++
      if (current && !original) added++
      if (!current && original) removed++
    }
    const words = [
      t(resource.label_key),
      t(resource.description_key || ''),
      ...resource.actions.map((action) => t(action.label_key)),
    ]
      .join(' ')
      .toLocaleLowerCase()
    if (query && !words.includes(query)) continue
    const legacyGroups: Record<string, string> = {
      audit: 'media',
      channel: 'models',
      task_plugin: 'system',
    }
    const groupKey =
      resource.group_key || legacyGroups[resource.resource] || 'other'
    let group = groups.get(groupKey)
    if (!group) {
      group = {
        key: groupKey,
        labelKey:
          SIDEBAR_MODULE_SECTIONS.find((section) => section.key === groupKey)
            ?.titleKey || 'Other permissions',
        resources: [],
      }
      groups.set(groupKey, group)
    }
    group.resources.push(resource)
  }
  const order = [
    'users',
    'finance',
    'media',
    'models',
    'content',
    'system',
    'other',
  ]
  const visibleGroups = [...groups.values()].sort(
    (a, b) => order.indexOf(a.key) - order.indexOf(b.key)
  )

  const changePermissions = (next: AdminPermissionMatrix) => {
    const result = { ...next }
    for (const resource of props.catalog.resources) {
      if (
        !resource.session_only ||
        !resource.actions.some((action) => action.action === 'write')
      ) {
        continue
      }
      const previous = props.value[resource.resource]
      const changed = next[resource.resource]
      if (changed?.write && !previous?.write) {
        result[resource.resource] = { ...changed, read: true }
      } else if (previous?.read && !changed?.read) {
        result[resource.resource] = { ...changed, write: false }
      }
    }
    props.onChange(result)
  }

  return (
    <div className='space-y-3'>
      <Input
        aria-label={t('Search administrator permissions')}
        placeholder={t('Search a feature or action')}
        value={search}
        onChange={(event) => setSearch(event.target.value)}
      />
      <div className='flex flex-wrap gap-2' role='status' aria-live='polite'>
        <Badge variant='secondary'>
          {t('{{count}} permissions granted', { count: selected })}
        </Badge>
        <Badge variant='outline'>
          {t('{{count}} grants added', { count: added })}
        </Badge>
        <Badge variant='outline'>
          {t('{{count}} grants removed', { count: removed })}
        </Badge>
      </div>
      {visibleGroups.length > 0 ? (
        <PermissionMatrix
          groups={visibleGroups}
          value={props.value}
          onChange={changePermissions}
          disabled={props.disabled}
          checkboxStyle
        />
      ) : (
        <EmptyState
          title={t('No matching permissions')}
          description={t('Try a different feature or action.')}
          className='min-h-28'
        />
      )}
      <p className='text-muted-foreground text-xs leading-relaxed'>
        {t(
          'Enabling editing also enables viewing. Removing viewing also removes editing.'
        )}
      </p>
      <p className='bg-muted/50 text-muted-foreground rounded-md p-3 text-xs leading-relaxed'>
        {t(
          'Upstream secrets, price changes, concurrency limits and system security remain restricted to the super administrator.'
        )}
      </p>
    </div>
  )
}
