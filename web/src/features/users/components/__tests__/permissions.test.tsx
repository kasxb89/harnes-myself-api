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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, expect, it, vi } from 'vitest'

import type {
  AdminPermissionMatrix,
  PermissionCatalog,
} from '@/lib/admin-permissions'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import type { User } from '../../types'
import { AdminPermissionEditor } from '../admin-permission-editor'
import { UsersMutateDrawer } from '../users-mutate-drawer'
import { UsersProvider } from '../users-provider'

const target: User = {
  id: 2,
  username: 'managed-admin',
  display_name: 'Managed admin',
  role: 10,
  status: 1,
  quota: 0,
  used_quota: 0,
  request_count: 0,
  group: 'default',
}
const label = "View other accounts' audit logs"
const description =
  'View audit records from user and admin roles. Root records are always excluded.'

function renderPermissions(
  viewerRole: number,
  allowed?: boolean,
  catalogState: 'ready' | 'error' | 'empty' = 'ready'
) {
  let failures = catalogState === 'error' ? 1 : 0
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'operator', role: viewerRole })
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/authz/catalog') {
      if (failures > 0) {
        failures--
        throw new Error('Permission catalog unavailable in fixture')
      }
      return {
        data: {
          success: true,
          data: {
            resources:
              catalogState === 'empty'
                ? []
                : [
                    {
                      resource: 'audit',
                      label_key: 'Audit Logs',
                      actions: [
                        {
                          action: 'read',
                          label_key: label,
                          description_key: description,
                        },
                      ],
                    },
                  ],
            roles: [{ key: 'admin', grants: { audit: { read: false } } }],
          },
        },
      }
    }
    if (url === '/api/group/') {
      return { data: { success: true, data: ['default'] } }
    }
    if (url === '/api/verify/methods') {
      return {
        data: {
          success: true,
          data: {
            scope: 'admin.user.update',
            methods: [{ method: '2fa', available: true }],
            oauth_providers: [],
            password_encryption_enabled: false,
          },
        },
      }
    }
    return {
      data: {
        success: true,
        data: {
          ...target,
          admin_permissions:
            allowed === undefined ? {} : { audit: { read: allowed } },
        },
      },
    }
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <UsersProvider>
        <UsersMutateDrawer
          open
          onOpenChange={() => undefined}
          currentRow={target}
        />
      </UsersProvider>
    </QueryClientProvider>
  )
  return get
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})

it.each([undefined, true])(
  'root can save an audit grant or revocation after step-up verification (previous=%s)',
  async (allowed) => {
    const put = vi
      .spyOn(api, 'put')
      .mockResolvedValue({ data: { success: true } })
    vi.spyOn(api, 'post').mockResolvedValue({
      data: {
        success: true,
        data: {
          proof_token: 'update-proof',
          method: '2fa',
          scope: 'admin.user.update',
          expires_at: Math.floor(Date.now() / 1000) + 60,
        },
      },
    })
    renderPermissions(100, allowed)
    await screen.findByDisplayValue('Managed admin')
    const toggle = await screen.findByRole('checkbox', { name: label })
    await waitFor(() =>
      expect(toggle).toHaveAttribute('aria-checked', String(!!allowed))
    )
    expect(toggle).toHaveAccessibleDescription(description)
    await userEvent.click(toggle)
    await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
    // The permission matrix changed, so the save waits for verification.
    expect(put).not.toHaveBeenCalled()
    await userEvent.type(
      await screen.findByLabelText('Authenticator code or backup code'),
      '123456'
    )
    await userEvent.click(screen.getByRole('button', { name: 'Verify' }))
    await waitFor(() =>
      expect(put).toHaveBeenCalledWith(
        '/api/user/',
        expect.objectContaining({
          id: 2,
          admin_permissions: { audit: { read: !allowed } },
        }),
        expect.objectContaining({
          headers: { 'X-Security-Proof': 'update-proof' },
          singleUseAuthorization: true,
        })
      )
    )
  }
)

it('root saving an administrator without changing permissions or password does not verify', async () => {
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  const get = renderPermissions(100, true)
  const displayName = await screen.findByDisplayValue('Managed admin')
  await userEvent.clear(displayName)
  await userEvent.type(displayName, 'Renamed admin')
  await userEvent.click(screen.getByRole('button', { name: 'Save changes' }))
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith(
      '/api/user/',
      expect.objectContaining({ id: 2, display_name: 'Renamed admin' }),
      {}
    )
  )
  expect(put.mock.calls[0][1]).not.toHaveProperty('admin_permissions')
  expect(get).not.toHaveBeenCalledWith('/api/verify/methods', expect.anything())
})

it('admin cannot edit the audit permission even when the catalog is available', async () => {
  renderPermissions(10)
  await screen.findByDisplayValue('Managed admin')
  expect(
    screen.queryByRole('checkbox', { name: label })
  ).not.toBeInTheDocument()
})

it('a failed catalog remains visible and blocks administrator save until retry succeeds', async () => {
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  renderPermissions(100, true, 'error')
  await screen.findByDisplayValue('Managed admin')
  expect(
    await screen.findByText('Unable to load administrator permissions')
  ).toBeVisible()
  expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled()
  expect(put).not.toHaveBeenCalled()
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Save changes' })).toBeEnabled()
  )
  expect(await screen.findByRole('checkbox', { name: label })).toBeChecked()
})

it('an empty catalog cannot silently replace a saved administrator permission matrix', async () => {
  const put = vi
    .spyOn(api, 'put')
    .mockResolvedValue({ data: { success: true } })
  renderPermissions(100, true, 'empty')
  await screen.findByDisplayValue('Managed admin')
  expect(
    await screen.findByText('No assignable permissions are available')
  ).toBeVisible()
  expect(screen.getByRole('button', { name: 'Save changes' })).toBeDisabled()
  expect(put).not.toHaveBeenCalled()
})

it('searching business groups preserves grants and editing automatically includes viewing', async () => {
  const catalog: PermissionCatalog = {
    resources: [
      {
        resource: 'customer_service',
        label_key: 'Customer service',
        group_key: 'content',
        session_only: true,
        actions: [
          {
            action: 'read',
            label_key: 'View',
            description_key: 'View QR settings',
          },
          {
            action: 'write',
            label_key: 'Edit',
            description_key: 'Upload QR settings',
          },
        ],
      },
      {
        resource: 'model_profits',
        label_key: 'Model profits',
        group_key: 'finance',
        actions: [
          {
            action: 'read',
            label_key: 'View',
            description_key: 'View model profits',
          },
        ],
      },
      {
        resource: 'channel',
        label_key: 'Channels',
        group_key: 'models',
        actions: [
          {
            action: 'secret_view',
            label_key: 'View upstream secrets',
            description_key: 'Root gate remains active',
            root_only: true,
          },
        ],
      },
    ],
    roles: [],
  }
  function EditorFixture() {
    const [value, setValue] = useState<AdminPermissionMatrix>({})
    return (
      <AdminPermissionEditor
        catalog={catalog}
        value={value}
        initialValue={{}}
        onChange={setValue}
      />
    )
  }
  render(<EditorFixture />)
  expect(
    screen.getByRole('checkbox', { name: /View upstream secrets/ })
  ).toHaveAttribute('aria-disabled', 'true')
  const search = screen.getByRole('textbox', {
    name: 'Search administrator permissions',
  })
  await userEvent.type(search, 'Customer service')
  expect(
    screen.queryByRole('group', { name: 'Model profits' })
  ).not.toBeInTheDocument()
  await userEvent.click(
    within(screen.getByRole('group', { name: 'Customer service' })).getByRole(
      'checkbox',
      { name: 'Edit' }
    )
  )
  expect(
    within(screen.getByRole('group', { name: 'Customer service' })).getByRole(
      'checkbox',
      { name: 'View' }
    )
  ).toBeChecked()
  expect(screen.getByText('2 permissions granted')).toBeVisible()
  await userEvent.clear(search)
  await userEvent.click(
    within(screen.getByRole('group', { name: 'Model profits' })).getByRole(
      'checkbox',
      { name: 'View' }
    )
  )
  expect(screen.getByText('3 permissions granted')).toBeVisible()
  const support = within(
    screen.getByRole('group', { name: 'Customer service' })
  )
  await userEvent.click(support.getByRole('checkbox', { name: 'View' }))
  expect(support.getByRole('checkbox', { name: 'Edit' })).not.toBeChecked()
  expect(screen.getByText('1 permissions granted')).toBeVisible()
})
