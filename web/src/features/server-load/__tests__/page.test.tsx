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
  act,
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import fr from '@/i18n/locales/fr.json'
import ja from '@/i18n/locales/ja.json'
import ru from '@/i18n/locales/ru.json'
import viLocale from '@/i18n/locales/vi.json'
import zhTW from '@/i18n/locales/zh-TW.json'
import zh from '@/i18n/locales/zh.json'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

import { ServerLoadPage } from '..'
import { serverLoadFixture } from './fixtures'

const originalAdapter = api.defaults.adapter
const clients: QueryClient[] = []
function setup() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <ServerLoadPage />
    </QueryClientProvider>
  )
}
beforeEach(async () => {
  await i18next.changeLanguage('en')
  useAuthStore.getState().auth.setUser({
    id: 7,
    username: 'operator',
    role: 10,
    permissions: { admin_permissions: { diagnostics: { read: true } } },
  })
  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('visible')
  vi.spyOn(navigator, 'onLine', 'get').mockReturnValue(true)
})
afterEach(async () => {
  cleanup()
  clients.splice(0).forEach((client) => client.clear())
  api.defaults.adapter = originalAdapter
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
  await i18next.changeLanguage('en')
})
it('shows capacity, actual samples and independently labelled process, container and database resources without nested cards', async () => {
  const fixture = serverLoadFixture(Math.floor(Date.now() / 1000))
  fixture.cpu.usage_percent = null
  api.defaults.adapter = async (config) => ({
    data: { success: true, data: fixture },
    status: 200,
    statusText: 'OK',
    headers: {},
    config,
  })
  setup()
  expect(
    await screen.findByRole('heading', { name: 'Server load' })
  ).toBeVisible()
  const cpu = await screen.findByRole('region', { name: 'Visible CPU usage' })
  expect(within(cpu).getByText('Not collected')).toBeVisible()
  expect(within(cpu).queryByRole('progressbar')).not.toBeInTheDocument()
  const memory = screen.getByRole('region', { name: 'Visible system memory' })
  expect(
    within(memory).getByText('3 GiB available / 8 GiB total')
  ).toBeVisible()
  expect(
    screen.getByText('Container view · host visibility is partial')
  ).toBeVisible()
  expect(screen.getByRole('region', { name: 'API process' })).toBeVisible()
  expect(
    screen.getByRole('region', { name: 'Container resources' })
  ).toBeVisible()
  expect(
    screen.getByRole('region', { name: 'Database connection pools' })
  ).toHaveTextContent('Not collected')
  expect(screen.getByText('Waiting for resource samples')).toBeVisible()
  await userEvent.click(screen.getByRole('button', { name: 'Memory' }))
  expect(
    await screen.findByRole('img', { name: 'Memory usage trend' })
  ).toBeVisible()
  expect(document.querySelector('[data-slot="card"]')).toBeNull()
  await userEvent.click(screen.getByRole('button', { name: 'Pause' }))
  expect(screen.getByRole('button', { name: 'Resume' })).toHaveAttribute(
    'aria-pressed',
    'true'
  )
})
it('keeps load failures visible and retries only on an explicit read action', async () => {
  let fail = true
  api.defaults.adapter = async (config) => ({
    data: fail
      ? { success: false, message: 'Sampling unavailable' }
      : {
          success: true,
          data: serverLoadFixture(Math.floor(Date.now() / 1000)),
        },
    status: 200,
    statusText: 'OK',
    headers: {},
    config,
  })
  setup()
  expect(await screen.findByText('Unable to read server load')).toBeVisible()
  fail = false
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(
    await screen.findByRole('region', { name: 'Resource overview' })
  ).toBeVisible()
})
it('shows threshold violations and distinguishes unlimited, unknown and process memory values', async () => {
  const fixture = serverLoadFixture(Math.floor(Date.now() / 1000))
  fixture.disk.used_percent = 95
  fixture.container.memory_limit_status = 'unlimited'
  fixture.container.memory_limit_bytes = null
  fixture.container.cpu_limit_status = 'unknown'
  fixture.container.cpu_limit_cores = null
  api.defaults.adapter = async (config) => ({
    data: { success: true, data: fixture },
    status: 200,
    statusText: 'OK',
    headers: {},
    config,
  })
  setup()
  const disk = await screen.findByRole('region', {
    name: 'Service storage space',
  })
  expect(
    within(disk).getByText('Observed value exceeds protection threshold')
  ).toBeVisible()
  expect(screen.getByText('No visible cgroup limit')).toBeVisible()
  expect(screen.getByText('Go runtime allocated memory')).toBeVisible()
  const container = screen.getByRole('region', { name: 'Container resources' })
  expect(within(container).getByText('Not collected')).toBeVisible()
})
it('denies direct rendering to an administrator without diagnostic read permission and sends no request', async () => {
  useAuthStore.getState().auth.setUser({
    id: 7,
    username: 'operator',
    role: 10,
    permissions: { admin_permissions: { operations: { read: true } } },
  })
  let calls = 0
  api.defaults.adapter = async () => {
    calls++
    throw new Error('Unexpected request')
  }
  setup()
  expect(screen.getByText('Permission denied')).toBeVisible()
  await waitFor(() => expect(calls).toBe(0))
})

it('renders Chinese resource labels and responds to all supported language changes without losing unknown semantics', async () => {
  const fixture = serverLoadFixture(Math.floor(Date.now() / 1000))
  fixture.cpu.usage_percent = null
  api.defaults.adapter = async (config) => ({
    data: { success: true, data: fixture },
    status: 200,
    statusText: 'OK',
    headers: {},
    config,
  })
  const resources = { zhCN: zh, zhTW, en, fr, ru, ja, vi: viLocale }
  for (const [language, resource] of Object.entries(resources)) {
    i18next.addResourceBundle(
      language,
      'translation',
      resource.translation,
      true,
      true
    )
  }
  setup()
  expect(
    await screen.findByRole('region', { name: 'Visible CPU usage' })
  ).toBeVisible()
  for (const [language, resource] of Object.entries(resources)) {
    await act(async () => {
      await i18next.changeLanguage(language)
    })
    expect(
      screen.getByRole('heading', { name: resource.translation['Server load'] })
    ).toBeVisible()
    const cpu = screen.getByRole('region', {
      name: resource.translation['Visible CPU usage'],
    })
    expect(
      within(cpu).getByText(resource.translation['Not collected'])
    ).toBeVisible()
    expect(within(cpu).queryByRole('progressbar')).not.toBeInTheDocument()
    expect(
      screen.getByRole('region', {
        name: resource.translation['Visible system memory'],
      })
    ).toHaveTextContent('GiB')
  }
})
it.each([
  { threshold: 0, usage: 95, enabled: true, warn: false },
  { threshold: 90, usage: 90, enabled: true, warn: false },
  { threshold: 90, usage: 90.9, enabled: true, warn: false },
  { threshold: 90, usage: 91, enabled: true, warn: true },
  { threshold: 150, usage: 95, enabled: true, warn: false },
  { threshold: 90, usage: 95, enabled: false, warn: false },
])(
  'reports the observed CPU threshold condition accurately for $threshold / $usage / enabled=$enabled',
  async ({ threshold, usage, enabled, warn }) => {
    const fixture = serverLoadFixture(Math.floor(Date.now() / 1000))
    fixture.cpu.usage_percent = usage
    fixture.thresholds.cpu_percent = threshold
    fixture.thresholds.guard_enabled = enabled
    api.defaults.adapter = async (config) => ({
      data: { success: true, data: fixture },
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    })
    setup()
    const cpu = await screen.findByRole('region', { name: 'Visible CPU usage' })
    expect(
      !!within(cpu).queryByText('Observed value exceeds protection threshold')
    ).toBe(warn)
    const protection = screen.getByRole('region', {
      name: 'Resource protection',
    })
    if (threshold === 0) {
      expect(within(protection).getByText('Disabled')).toBeVisible()
    }
    if (threshold === 150) {
      expect(within(protection).getByText('150%')).toBeVisible()
    }
  }
)
