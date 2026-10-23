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
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'

import { ThemeProvider } from '@/context/theme-provider'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { UpstreamTopupsPage } from '..'

vi.mock('@visactor/react-vchart', () => ({ VChart: () => null }))
vi.mock('@visactor/vchart', () => ({
  ThemeManager: { setCurrentTheme: () => undefined },
}))

const record = {
  id: 1,
  upstream: 'Example supplier',
  amount_cents: 100,
  currency: 'CNY',
  paid_at: 1792771200,
  note: 'Recorded payment',
}
const overview = {
  spent_cents: { CNY: 100 },
  received: { CNY: 20 },
  consumed_quota: 0,
  consumed_quota_today: 0,
  active_users_today: 0,
  calls_today: 0,
  users: { total: 1, new_today: 0, balance_quota: 0 },
  topups: {
    money_total: 20,
    count_total: 1,
    users_total: 1,
    money_today: 0,
    count_today: 0,
    users_today: 0,
  },
  topup_users: [],
  recent_topups: [],
  daily: [],
}

function renderFinance(capabilities: Record<string, Record<string, boolean>>) {
  useAuthStore.getState().auth.setUser({
    id: 4,
    username: 'finance-reader',
    role: 10,
    permissions: { admin_permissions: capabilities },
  })
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/operations/overview') {
      return { data: { success: true, data: overview } }
    }
    if (url === '/api/upstream-topup/') {
      return {
        data: {
          success: true,
          data: { items: [record], spent_cents: { CNY: 100 } },
        },
      }
    }
    throw new Error('Unexpected fixture request')
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <ThemeProvider>
        <UpstreamTopupsPage />
      </ThemeProvider>
    </QueryClientProvider>
  )
  return get
}

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})

it('upstream-only viewing loads the records and cannot edit or read business totals', async () => {
  const get = renderFinance({
    upstream_topups: { read: true, write: false },
    operations: { read: false },
  })
  expect(await screen.findByText('Example supplier')).toBeVisible()
  expect(screen.getByRole('button', { name: 'Record a top-up' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Edit' })).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Delete' })).toBeDisabled()
  expect(screen.queryByText('Today')).not.toBeInTheDocument()
  expect(get).not.toHaveBeenCalledWith('/api/operations/overview')
})

it('business-only viewing loads its own endpoint without showing upstream payment details', async () => {
  const get = renderFinance({
    operations: { read: true },
    upstream_topups: { read: false, write: false },
  })
  await waitFor(() =>
    expect(get).toHaveBeenCalledWith('/api/operations/overview')
  )
  expect(screen.getByRole('heading', { name: 'Today' })).toBeVisible()
  expect(screen.queryByText('Example supplier')).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Record a top-up' })
  ).not.toBeInTheDocument()
  expect(get).not.toHaveBeenCalledWith('/api/upstream-topup/')
})

it('removing access hides already-loaded payment details and does not start a broader query', async () => {
  const get = renderFinance({ upstream_topups: { read: true } })
  expect(await screen.findByText('Example supplier')).toBeVisible()
  act(() => {
    useAuthStore.getState().auth.setUser({
      id: 4,
      username: 'finance-reader',
      role: 10,
      permissions: { admin_permissions: {} },
    })
  })
  expect(screen.queryByText('Example supplier')).not.toBeInTheDocument()
  expect(get).not.toHaveBeenCalledWith('/api/operations/overview')
})

it('an administrator with no finance grants does not issue either query', () => {
  const get = renderFinance({})
  expect(get).not.toHaveBeenCalled()
  expect(
    screen.queryByRole('button', { name: 'Record a top-up' })
  ).not.toBeInTheDocument()
})
