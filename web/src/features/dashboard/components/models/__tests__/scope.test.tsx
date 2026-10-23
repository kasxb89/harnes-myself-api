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
import { act, render, screen, waitFor } from '@testing-library/react'
import { beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { LogStatCards } from '../log-stat-cards'

function setPermissions(read: boolean) {
  useAuthStore.getState().auth.setUser({
    id: 2,
    username: 'operator',
    role: ROLE.ADMIN,
    permissions: { admin_permissions: { operations: { read } } },
  })
}
beforeEach(() => setPermissions(false))

test('a delegated administrator without operations permission requests only its own dashboard data', async () => {
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValue({ data: { success: true, data: [] } })
  render(<LogStatCards />)
  await waitFor(() => expect(get).toHaveBeenCalled())
  expect(get.mock.calls.every(([url]) => url === '/api/data/self')).toBe(true)
})

test('revoking operations clears visible global totals before its own data finishes loading', async () => {
  setPermissions(true)
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/data/self') return new Promise(() => undefined)
    return {
      data: {
        success: true,
        data: [
          {
            created_at: 1792380605,
            model_name: 'model',
            quota: 0,
            count: 987654,
            token_used: 0,
          },
        ],
      },
    }
  })
  render(<LogStatCards />)
  expect(await screen.findByTitle('987,654')).toBeVisible()
  act(() => setPermissions(false))
  await waitFor(() =>
    expect(get.mock.calls.some(([url]) => url === '/api/data/self')).toBe(true)
  )
  expect(screen.queryByTitle('987,654')).not.toBeInTheDocument()
})
