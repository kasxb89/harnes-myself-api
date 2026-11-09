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
import {
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { CostRulesDialog } from '../components/cost-rules-dialog'

afterEach(() => {
  cleanup()
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
})

function setup(role: number) {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'fixture', role })
  const root = createRootRoute({ component: CostRulesDialog })
  const router = createRouter({
    routeTree: root,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  render(<RouterProvider router={router} />)
}

it('routes procurement editing to the single unified module without fetching or writing a second cost form', async () => {
  const get = vi.spyOn(api, 'get')
  const put = vi.spyOn(api, 'put')
  setup(ROLE.SUPER_ADMIN)
  const entry = await screen.findByRole('link', {
    name: 'Configure upstream costs',
  })
  expect(entry).toHaveAttribute('href', '/client-model-config?tab=unified')
  expect(screen.queryByRole('dialog')).toBeNull()
  expect(get).not.toHaveBeenCalled()
  expect(put).not.toHaveBeenCalled()
})

it('keeps the root-only cost editor entry unavailable to regular admins', async () => {
  setup(ROLE.ADMIN)
  expect(
    screen.queryByRole('link', { name: 'Configure upstream costs' })
  ).toBeNull()
})
