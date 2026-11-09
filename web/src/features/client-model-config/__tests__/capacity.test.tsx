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
  createMemoryHistory,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { SettingsPageProvider } from '@/features/system-settings/components/settings-page-context'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { ClientModelConfigPage } from '..'
import { ClientModelCapacitySection } from '../components/capacity-section'
import { capacityFormSchema } from '../lib/capacity'
import type { ClientModelCapacityCatalog } from '../types'

let catalog: ClientModelCapacityCatalog
const clients: QueryClient[] = []

// The old form/API remains compatible without exposing a second editor.
function LegacyCapacityHarness() {
  const [actions, setActions] = useState<HTMLDivElement | null>(null)
  return (
    <SettingsPageProvider
      actionsContainer={actions}
      suppressSectionHeader={false}
    >
      <div ref={setActions} />
      <ClientModelCapacitySection
        active
        models={[
          {
            id: 'yunshu-flash',
            display_name: 'Cloud Flash',
            chat_eligible: true,
          },
        ]}
        onDirtyChange={() => undefined}
      />
    </SettingsPageProvider>
  )
}

beforeEach(() => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'root', role: ROLE.SUPER_ADMIN })
  catalog = {
    revision: 'initial-revision',
    routes: ['default', 'product', 'yunshu-managed'].map((group) => ({
      model: 'yunshu-flash',
      group,
      channel_id: 12,
      channel_name: 'DeepSeek official',
      route_model: 'yunshu-flash',
      budget_field: 'max_tokens',
      source: '',
      verified_at: '',
      configured: false,
      valid: false,
    })),
  }
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/option/model_capacity/catalog') {
      return { data: { success: true, data: structuredClone(catalog) } }
    }
    if (url === '/api/client-model-config') {
      return {
        data: {
          success: true,
          data: {
            schema_version: 1,
            default_chat_model: 'yunshu-flash',
            model_order: [],
          },
        },
      }
    }
    if (url === '/api/client-model-config/models') {
      return {
        data: {
          success: true,
          data: {
            models: [
              {
                id: 'yunshu-flash',
                display_name: 'Cloud Flash',
                chat_eligible: true,
              },
            ],
          },
        },
      }
    }
    if (url === '/api/client-model-config/presentation') {
      return {
        data: { success: true, data: { schema_version: 1, models: {} } },
      }
    }
    if (url === '/api/client-model-config/media-defaults') {
      return {
        data: {
          success: true,
          data: {
            schema_version: 1,
            default_image_model: '',
            default_video_model: '',
          },
        },
      }
    }
    throw new Error(`Unexpected request ${url}`)
  })
})

afterEach(() => {
  clients.splice(0).forEach((client) => client.clear())
})

async function setup(activate = true) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  clients.push(client)
  const root = createRootRoute({
    component: activate ? LegacyCapacityHarness : ClientModelConfigPage,
  })
  const router = createRouter({
    routeTree: root,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  const user = userEvent.setup()
  if (!activate) {
    await screen.findByRole('combobox', { name: 'Default chat model' })
    return user
  }
  await screen.findByRole('textbox', { name: 'Context window (tokens)' })
  return user
}

function fillCapacities(context = '1048576', output = '32768') {
  fireEvent.change(
    screen.getByRole('textbox', { name: 'Context window (tokens)' }),
    { target: { value: context } }
  )
  fireEvent.change(
    screen.getByRole('textbox', { name: 'Maximum output tokens' }),
    { target: { value: output } }
  )
  fireEvent.change(
    screen.getByRole('textbox', { name: 'Upstream specification source' }),
    { target: { value: 'https://api.deepseek.com/models' } }
  )
  fireEvent.change(screen.getByLabelText('Verification date'), {
    target: { value: '2026-11-07' },
  })
}

test('saving a model channel updates each displayed group and uses the loaded revision', async () => {
  const patch = vi.spyOn(api, 'patch').mockImplementation(async (_url, raw) => {
    const body = raw as {
      revision: string
      routes: ClientModelCapacityCatalog['routes']
    }
    catalog = {
      revision: 'saved-revision',
      routes: body.routes.map((row) => ({
        ...row,
        configured: true,
        valid: true,
        channel_name: 'DeepSeek official',
      })),
    }
    return { data: { success: true, data: structuredClone(catalog) } }
  })
  const user = await setup()
  fillCapacities()
  await user.click(
    screen.getByRole('button', { name: 'Save model capacities' })
  )
  await waitFor(() => expect(patch).toHaveBeenCalledOnce())
  const savedCall = patch.mock.calls[0]
  if (!savedCall) throw new Error('Capacity save was not received')
  const [url, body] = savedCall
  expect(url).toBe('/api/option/model_capacity')
  expect(body).toEqual({
    revision: 'initial-revision',
    routes: ['default', 'product', 'yunshu-managed'].map((group) => ({
      model: 'yunshu-flash',
      group,
      channel_id: 12,
      route_model: 'yunshu-flash',
      budget_field: 'max_tokens',
      context_window: 1048576,
      max_output_tokens: 32768,
      source: 'https://api.deepseek.com/models',
      verified_at: '2026-11-07',
    })),
  })
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Save model capacities' })
    ).toBeDisabled()
  )
  expect(
    screen.getByRole('textbox', { name: 'Context window (tokens)' })
  ).toHaveValue('1048576')
})

test('output exceeding the context window stays in the draft and does not save', async () => {
  const patch = vi.spyOn(api, 'patch')
  const user = await setup()
  fillCapacities('8192', '32768')
  await user.click(
    screen.getByRole('button', { name: 'Save model capacities' })
  )
  expect(
    await screen.findByText('Maximum output must not exceed the context window')
  ).toBeVisible()
  expect(patch).not.toHaveBeenCalled()
})

test('a stale revision preserves the draft and requires reload before another save', async () => {
  const patch = vi
    .spyOn(api, 'patch')
    .mockRejectedValue({ isAxiosError: true, response: { status: 409 } })
  const user = await setup()
  fillCapacities()
  await user.click(
    screen.getByRole('button', { name: 'Save model capacities' })
  )
  expect(
    await screen.findByText(
      'Model routes or capacities changed. Reload and review before saving.'
    )
  ).toBeVisible()
  expect(
    screen.getByRole('textbox', { name: 'Context window (tokens)' })
  ).toHaveValue('1048576')
  expect(
    screen.getByRole('button', { name: 'Save model capacities' })
  ).toBeDisabled()
  expect(patch).toHaveBeenCalledOnce()
  catalog.revision = 'someone-elses-revision'
  catalog.routes = catalog.routes.map((row) => ({
    ...row,
    context_window: 131072,
    max_output_tokens: 16384,
    source: 'supplier',
    verified_at: '2026-11-07',
    configured: true,
    valid: true,
  }))
  await user.click(screen.getByRole('button', { name: 'Reload configuration' }))
  await waitFor(() =>
    expect(
      screen.getByRole('textbox', { name: 'Context window (tokens)' })
    ).toHaveValue('131072')
  )
})

test('an unavailable catalog offers a retry without any editable token fields', async () => {
  const get = vi.mocked(api.get)
  const previous = get.getMockImplementation()
  if (!previous) throw new Error('API fixture is missing')
  get.mockImplementation(async (url, ...args) => {
    if (url === '/api/option/model_capacity/catalog') throw new Error('offline')
    return previous(url, ...args)
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  const root = createRootRoute({ component: LegacyCapacityHarness })
  const router = createRouter({
    routeTree: root,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  expect(
    await screen.findByText('Failed to load model capacities')
  ).toBeVisible()
  expect(
    screen.queryByRole('textbox', { name: 'Context window (tokens)' })
  ).toBeNull()
})

test('token inputs reject suffixes, decimals, missing evidence and overflow', () => {
  const valid = {
    context_window: '1048576',
    max_output_tokens: '32768',
    source: 'supplier specification',
    verified_at: '2026-11-07',
  }
  expect(capacityFormSchema.safeParse(valid).success).toBe(true)
  expect(
    capacityFormSchema.safeParse({ ...valid, max_output_tokens: '' }).success
  ).toBe(true)
  expect(
    capacityFormSchema.safeParse({ ...valid, context_window: '' }).success
  ).toBe(true)
  expect(
    capacityFormSchema.safeParse({
      ...valid,
      context_window: '',
      max_output_tokens: '',
    }).success
  ).toBe(false)
  for (const context_window of ['1M', '0', '-1', '1.5', '2147483648']) {
    expect(
      capacityFormSchema.safeParse({ ...valid, context_window }).success
    ).toBe(false)
  }
  expect(capacityFormSchema.safeParse({ ...valid, source: '' }).success).toBe(
    false
  )
})

test('a blank unverified output is omitted rather than saved as zero', async () => {
  const patch = vi.spyOn(api, 'patch').mockImplementation(async () => ({
    data: { success: true, data: structuredClone(catalog) },
  }))
  const user = await setup()
  fillCapacities('1048576', '')
  await user.click(
    screen.getByRole('button', { name: 'Save model capacities' })
  )
  await waitFor(() => expect(patch).toHaveBeenCalledOnce())
  const request = patch.mock.calls[0]?.[1] as {
    routes: Record<string, unknown>[]
  }
  expect(request.routes).toHaveLength(3)
  for (const route of request.routes) {
    expect(route.context_window).toBe(1048576)
    expect(route).not.toHaveProperty('max_output_tokens')
  }
})

test('models requiring complete capacity metadata cannot save a missing field', async () => {
  catalog.routes = catalog.routes.map((route) => ({
    ...route,
    requires_full_capacity: true,
  }))
  const patch = vi.spyOn(api, 'patch')
  const user = await setup()
  fillCapacities('1048576', '')
  await user.click(
    screen.getByRole('button', { name: 'Save model capacities' })
  )
  const input = screen.getByRole('textbox', { name: 'Context window (tokens)' })
  await waitFor(() => expect(input).toHaveAttribute('aria-invalid', 'true'))
  expect(patch).not.toHaveBeenCalled()
})

test('regular administrators cannot see or request root-only capacities', async () => {
  useAuthStore.getState().auth.setUser({
    id: 2,
    username: 'admin',
    role: ROLE.ADMIN,
    permissions: {
      admin_permissions: { client_model_config: { read: true, write: true } },
    },
  })
  await setup(false)
  expect(
    screen.queryByRole('tab', { name: 'Context and output limits' })
  ).toBeNull()
  expect(
    vi
      .mocked(api.get)
      .mock.calls.some(([url]) => url === '/api/option/model_capacity/catalog')
  ).toBe(false)
})

test('removing an edited route during refresh preserves its draft until discard', async () => {
  const patch = vi.spyOn(api, 'patch')
  const user = await setup()
  fillCapacities()
  catalog = { revision: 'route-removed', routes: [] }
  const client = clients.at(-1)
  if (!client) throw new Error('Query client is missing')
  await act(async () => {
    await client.refetchQueries({ queryKey: ['client-model-capacities'] })
  })
  expect(
    await screen.findByText(
      'This model route is unavailable. Discard changes and select an available route.'
    )
  ).toBeVisible()
  expect(
    screen.getByRole('textbox', { name: 'Context window (tokens)' })
  ).toHaveValue('1048576')
  expect(
    screen.getByRole('button', { name: 'Save model capacities' })
  ).toBeDisabled()
  const input = screen.getByRole('textbox', { name: 'Context window (tokens)' })
  const form = input.closest('form')
  if (!form) throw new Error('Capacity form is missing')
  await act(async () => fireEvent.submit(form))
  expect(patch).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Reload configuration' }))
  expect(
    await screen.findByText('No available chat model routes')
  ).toBeVisible()
  expect(
    screen.queryByRole('textbox', { name: 'Context window (tokens)' })
  ).toBeNull()
})
