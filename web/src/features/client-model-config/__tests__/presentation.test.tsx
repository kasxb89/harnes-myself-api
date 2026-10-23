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
  createRoute,
  Outlet,
  createRootRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { ClientModelConfigPage } from '..'
import { getClientModelPresentation } from '../api'
import {
  clientModelPresentationSchema,
  presentationEditorSchema,
} from '../lib/presentation'
import type { ClientModelConfig, ClientModelPresentation } from '../types'

const presentationUrl = '/api/client-model-config/presentation'
const initialConfig: ClientModelConfig = {
  schema_version: 1,
  default_chat_model: '',
  model_order: [],
}
const catalog = [
  { id: 'deepseek-chat', display_name: 'DeepSeek Chat', chat_eligible: true },
  { id: 'image-model', display_name: 'Image', chat_eligible: false },
]
const clients: QueryClient[] = []
let saved: ClientModelPresentation
let config: ClientModelConfig

beforeEach(() => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'root', role: ROLE.SUPER_ADMIN })
  saved = { schema_version: 1, models: {} }
  config = structuredClone(initialConfig)
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === presentationUrl) return { data: { success: true, data: saved } }
    if (url === '/api/client-model-config') {
      return { data: { success: true, data: config } }
    }
    return { data: { success: true, data: { models: catalog } } }
  })
  vi.spyOn(api, 'put').mockImplementation(async (url, body) => {
    if (url === presentationUrl) {
      saved = body as ClientModelPresentation
      return { data: { success: true, data: saved } }
    }
    config = body as ClientModelConfig
    return { data: { success: true, data: config } }
  })
})
afterEach(() => {
  clients.splice(0).forEach((client) => client.clear())
})

async function setup() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  clients.push(client)
  const route = createRootRoute({ component: Outlet })
  const page = createRoute({
    getParentRoute: () => route,
    path: '/',
    component: ClientModelConfigPage,
  })
  const other = createRoute({
    getParentRoute: () => route,
    path: '/about',
    component: () => <p>Other page</p>,
  })
  const router = createRouter({
    routeTree: route.addChildren([page, other]),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await router.load()
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  await screen.findByRole('combobox', { name: 'Default chat model' })
  await userEvent.click(
    screen.getByRole('tab', { name: 'Descriptions and badges' })
  )
  await screen.findByRole('table', { name: 'Model descriptions and badges' })
  return { client, router }
}

async function edit(id = 'deepseek-chat') {
  await userEvent.click(
    screen.getByRole('button', { name: `Edit presentation for ${id}` })
  )
  return screen.findByRole('dialog', {
    name: 'Edit model description and badge',
  })
}
async function customDescription(text: string) {
  await userEvent.selectOptions(
    screen.getByRole('combobox', { name: 'Description display' }),
    'custom'
  )
  fireEvent.change(
    screen.getByRole('textbox', { name: 'Custom description' }),
    { target: { value: text } }
  )
}
async function apply() {
  await userEvent.click(screen.getByRole('button', { name: 'Apply to draft' }))
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
}
async function save() {
  await userEvent.click(
    screen.getByRole('button', { name: 'Save descriptions and badges' })
  )
}

test('independent description override and hidden badge save only the presentation resource and preserve unknown exact IDs', async () => {
  saved = JSON.parse(
    '{"schema_version":1,"models":{"__proto__":{"description":"kept"},"retired-model":{"badge":"legacy"}}}'
  )
  await setup()
  await edit()
  await customDescription('<b>Plain text</b>\nSecond line')
  await userEvent.selectOptions(
    screen.getByRole('combobox', { name: 'Badge display' }),
    'hidden'
  )
  await apply()
  expect(screen.getByText('<b>Plain text</b> Second line')).toBeVisible()
  expect(
    screen.queryByRole('button', { name: 'Save client model configuration' })
  ).not.toBeInTheDocument()
  await save()
  await waitFor(() =>
    expect(api.put).toHaveBeenCalledWith(presentationUrl, {
      schema_version: 1,
      models: JSON.parse(
        '{"__proto__":{"description":"kept"},"retired-model":{"badge":"legacy"},"deepseek-chat":{"description":"<b>Plain text</b>\\nSecond line","badge":""}}'
      ),
    })
  )
  expect(Object.hasOwn(saved.models, '__proto__')).toBe(true)
  expect(config).toEqual(initialConfig)
})

test('returning both fields to client defaults removes only that model while retaining other presentation entries', async () => {
  saved.models = {
    'deepseek-chat': { description: '', badge: 'New' },
    'retired-model': { description: 'Keep' },
  }
  await setup()
  await edit()
  await userEvent.selectOptions(
    screen.getByRole('combobox', { name: 'Description display' }),
    'default'
  )
  await userEvent.selectOptions(
    screen.getByRole('combobox', { name: 'Badge display' }),
    'default'
  )
  await apply()
  await save()
  await waitFor(() =>
    expect(saved.models).toEqual({ 'retired-model': { description: 'Keep' } })
  )
})

test('presentation save and discard leave unsaved default and sort drafts intact across tab switches', async () => {
  await setup()
  await userEvent.click(screen.getByRole('tab', { name: 'Defaults and order' }))
  await userEvent.type(
    screen.getByRole('textbox', { name: 'Sort number for deepseek-chat' }),
    '8'
  )
  await userEvent.click(
    screen.getByRole('tab', { name: 'Descriptions and badges' })
  )
  await edit()
  await customDescription('Draft')
  await apply()
  await save()
  await waitFor(() =>
    expect(saved.models['deepseek-chat']?.description).toBe('Draft')
  )
  await edit()
  await customDescription('Discard')
  await apply()
  await userEvent.click(
    screen.getByRole('button', {
      name: 'Discard description and badge changes',
    })
  )
  expect(screen.getByText('Draft')).toBeVisible()
  await userEvent.click(screen.getByRole('tab', { name: 'Defaults and order' }))
  expect(
    screen.getByRole('textbox', { name: 'Sort number for deepseek-chat' })
  ).toHaveValue('8')
  await userEvent.click(
    screen.getByRole('button', { name: 'Save client model configuration' })
  )
  await waitFor(() => expect(config.model_order).toEqual(['deepseek-chat']))
  expect(saved.models['deepseek-chat'].description).toBe('Draft')
})

test('save failure preserves the draft and exposes an enabled retry action until a successful save', async () => {
  await setup()
  await edit()
  await customDescription('Retry me')
  await apply()
  vi.mocked(api.put).mockResolvedValueOnce({
    data: {
      success: false,
      message: 'Failed to save client model presentation',
    },
  })
  await save()
  expect(
    await screen.findByText(
      'Saving failed. Your draft is preserved. Retry saving when ready.'
    )
  ).toBeVisible()
  expect(screen.getByText('Retry me')).toBeVisible()
  expect(
    screen.getByRole('button', { name: 'Save descriptions and badges' })
  ).toBeEnabled()
  await save()
  await waitFor(() =>
    expect(saved.models['deepseek-chat']?.description).toBe('Retry me')
  )
  expect(
    screen.getByRole('button', { name: 'Save descriptions and badges' })
  ).toBeDisabled()
})

test('pristine refetch accepts new metadata while dirty draft survives successful and failed refetches', async () => {
  const { client } = await setup()
  saved = {
    schema_version: 1,
    models: { 'deepseek-chat': { description: 'Remote' } },
  }
  await act(() =>
    client.invalidateQueries({ queryKey: ['client-model-presentation'] })
  )
  expect(await screen.findByText('Remote')).toBeVisible()
  expect(
    screen.getByRole('button', { name: 'Save descriptions and badges' })
  ).toBeDisabled()
  await edit()
  await customDescription('Local')
  await apply()
  saved = {
    schema_version: 1,
    models: { 'deepseek-chat': { description: 'New remote' } },
  }
  await act(() =>
    client.invalidateQueries({ queryKey: ['client-model-presentation'] })
  )
  expect(screen.getByText('Local')).toBeVisible()
  vi.mocked(api.get).mockRejectedValueOnce(new Error('offline'))
  await act(() =>
    client.invalidateQueries({ queryKey: ['client-model-presentation'] })
  )
  expect(screen.getByText('Local')).toBeVisible()
  await userEvent.click(
    screen.getByRole('button', {
      name: 'Discard description and badge changes',
    })
  )
  expect(screen.getByText('New remote')).toBeVisible()
})

test('catalog failure preserves editable retired metadata', async () => {
  saved.models = { 'retired-model': { badge: '' } }
  vi.mocked(api.get).mockImplementation(async (url) => {
    if (url === presentationUrl) return { data: { success: true, data: saved } }
    if (url === '/api/client-model-config') {
      return { data: { success: true, data: config } }
    }
    throw new Error('offline')
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  const root = createRootRoute({ component: ClientModelConfigPage })
  const router = createRouter({
    routeTree: root,
    history: createMemoryHistory(),
  })
  await router.load()
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  await userEvent.click(
    await screen.findByRole('tab', { name: 'Descriptions and badges' })
  )
  expect(
    await screen.findByText(
      'The model catalog is unavailable. Saved descriptions and badges remain editable.'
    )
  ).toBeVisible()
  await edit('retired-model')
  await customDescription('Still editable')
  await apply()
  await save()
  await waitFor(() =>
    expect(saved.models['retired-model']).toEqual({
      description: 'Still editable',
      badge: '',
    })
  )
})

test('pending save disables edits, save and discard until its response arrives', async () => {
  await setup()
  await edit()
  await customDescription('Pending')
  await apply()
  let finish!: (response: unknown) => void
  vi.mocked(api.put).mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        finish = resolve
      })
  )
  await save()
  expect(screen.getByRole('button', { name: 'Saving...' })).toBeDisabled()
  expect(
    screen.getByRole('button', {
      name: 'Discard description and badge changes',
    })
  ).toBeDisabled()
  expect(
    screen.getByRole('button', { name: 'Edit presentation for deepseek-chat' })
  ).toBeDisabled()
  await act(async () => {
    finish({
      data: {
        success: true,
        data: {
          schema_version: 1,
          models: { 'deepseek-chat': { description: 'Pending' } },
        },
      },
    })
  })
})

test('dialog keyboard controls expose independent display modes, focus, field errors and literal text', async () => {
  await setup()
  const dialog = await edit()
  const description = within(dialog).getByRole('combobox', {
    name: 'Description display',
  })
  description.focus()
  expect(description).toHaveFocus()
  await userEvent.selectOptions(description, 'custom')
  await userEvent.click(screen.getByRole('button', { name: 'Apply to draft' }))
  expect(
    await screen.findByText('Enter custom text or choose another display mode.')
  ).toBeVisible()
  expect(
    screen.getByRole('textbox', { name: 'Custom description' })
  ).toHaveAttribute('aria-invalid', 'true')
  fireEvent.change(
    screen.getByRole('textbox', { name: 'Custom description' }),
    { target: { value: 'x'.repeat(501) } }
  )
  await userEvent.click(screen.getByRole('button', { name: 'Apply to draft' }))
  expect(
    await screen.findByText('Description must be 500 characters or fewer.')
  ).toBeVisible()
  await userEvent.keyboard('{Escape}')
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  expect(
    screen.getByRole('button', { name: 'Edit presentation for deepseek-chat' })
  ).toHaveFocus()
})

test('presentation validation preserves valid control text and exact prototype IDs while enforcing byte, UTF16 and single-line limits', async () => {
  const valid = JSON.parse(
    '{"schema_version":1,"models":{"__proto__":{"description":"a\\u000bb","badge":"😀"}}}'
  )
  vi.mocked(api.get).mockResolvedValueOnce({
    data: { success: true, data: valid },
  })
  expect(
    Object.hasOwn((await getClientModelPresentation()).models, '__proto__')
  ).toBe(true)
  expect(
    clientModelPresentationSchema.safeParse({
      schema_version: 1,
      models: {
        model: { description: '😀'.repeat(250), badge: '😀'.repeat(16) },
      },
    }).success
  ).toBe(true)
  for (const entry of [
    { description: null },
    { description: '😀'.repeat(251) },
    { badge: '😀'.repeat(17) },
    { badge: 'a\nb' },
    { badge: 'a\u0085b' },
  ]) {
    expect(
      clientModelPresentationSchema.safeParse({
        schema_version: 1,
        models: { model: entry },
      }).success
    ).toBe(false)
  }
  expect(
    clientModelPresentationSchema.safeParse({
      schema_version: 1,
      models: { ['模'.repeat(86)]: {} },
    }).success
  ).toBe(false)
  expect(
    clientModelPresentationSchema.safeParse({
      schema_version: 1,
      models: { 'id\u0085': {} },
    }).success
  ).toBe(false)
  expect(
    presentationEditorSchema.safeParse({
      description: { mode: 'default', text: '' },
      badge: { mode: 'custom', text: 'a\u2028b' },
    }).success
  ).toBe(false)
})

test('presentation load failure offers retry without exposing an editable empty map', async () => {
  let failed = true
  vi.mocked(api.get).mockImplementation(async (url) => {
    if (url === presentationUrl) {
      if (failed) throw new Error('offline')
      return { data: { success: true, data: saved } }
    }
    if (url === '/api/client-model-config') {
      return { data: { success: true, data: config } }
    }
    return { data: { success: true, data: { models: catalog } } }
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  const route = createRootRoute({ component: ClientModelConfigPage })
  const router = createRouter({
    routeTree: route,
    history: createMemoryHistory(),
  })
  await router.load()
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  await userEvent.click(
    await screen.findByRole('tab', { name: 'Descriptions and badges' })
  )
  expect(
    await screen.findByText('Failed to load client model presentation')
  ).toBeVisible()
  expect(
    screen.queryByRole('table', { name: 'Model descriptions and badges' })
  ).not.toBeInTheDocument()
  failed = false
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(
    await screen.findByRole('table', { name: 'Model descriptions and badges' })
  ).toBeVisible()
})

test('search filters visible rows without deleting hidden configuration when another model is saved', async () => {
  saved.models = { 'retired-model': { badge: 'Keep' } }
  await setup()
  const search = screen.getByRole('searchbox', { name: 'Search model IDs' })
  await userEvent.type(search, 'deepseek')
  expect(
    screen.queryByRole('button', {
      name: 'Edit presentation for retired-model',
    })
  ).not.toBeInTheDocument()
  await edit()
  await customDescription('Search edit')
  await apply()
  await save()
  await waitFor(() =>
    expect(saved.models['retired-model']).toEqual({ badge: 'Keep' })
  )
  await userEvent.clear(search)
  expect(
    screen.getByRole('button', { name: 'Edit presentation for retired-model' })
  ).toBeEnabled()
  expect(
    screen.getByRole('table', { name: 'Model descriptions and badges' })
  ).toHaveClass('table-fixed')
})

test('adding a 257th presentation blocks saving until a retired entry is reset to client defaults', async () => {
  saved.models = Object.fromEntries(
    Array.from({ length: 256 }, (_, index) => [
      `retired-${index}`,
      { description: 'Old' },
    ])
  )
  await setup()
  await edit()
  await customDescription('Extra')
  await apply()
  expect(
    screen.getByRole('button', { name: 'Save descriptions and badges' })
  ).toBeDisabled()
  expect(
    screen.getByText(
      'You can configure up to 256 model presentations. Reset a model to client defaults to free a slot.'
    )
  ).toBeVisible()
  await edit('retired-0')
  await userEvent.selectOptions(
    screen.getByRole('combobox', { name: 'Description display' }),
    'default'
  )
  await apply()
  await save()
  await waitFor(() => expect(Object.keys(saved.models)).toHaveLength(256))
  expect(saved.models).not.toHaveProperty('retired-0')
  expect(saved.models['deepseek-chat']).toEqual({ description: 'Extra' })
})

test('presentation exact IDs preserve Go-valid BOM endpoints and reject Unicode whitespace endpoints', async () => {
  const valid = {
    schema_version: 1,
    models: {
      '\uFEFFmodel': { badge: 'Leading' },
      'model\uFEFF': { description: 'Trailing' },
    },
  }
  vi.mocked(api.get).mockResolvedValueOnce({
    data: { success: true, data: valid },
  })
  expect((await getClientModelPresentation()).models).toEqual(valid.models)
  for (const id of [
    '\u00A0model',
    'model\u00A0',
    '\u0085model',
    'model\u3000',
  ]) {
    expect(
      clientModelPresentationSchema.safeParse({
        schema_version: 1,
        models: { [id]: {} },
      }).success
    ).toBe(false)
  }
})

test('leaving with both resources dirty opens one confirmation and cancellation retains both drafts', async () => {
  const { router } = await setup()
  await edit()
  await customDescription('Keep metadata')
  await apply()
  await userEvent.click(screen.getByRole('tab', { name: 'Defaults and order' }))
  await userEvent.type(
    screen.getByRole('textbox', { name: 'Sort number for deepseek-chat' }),
    '9'
  )
  act(() => {
    void router.navigate({ to: '/about' })
  })
  expect(
    await screen.findByRole('alertdialog', { name: 'Unsaved changes' })
  ).toBeVisible()
  expect(screen.getAllByRole('alertdialog')).toHaveLength(1)
  await userEvent.click(screen.getByRole('button', { name: 'Stay' }))
  await waitFor(() =>
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  )
  expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
  expect(
    screen.getByRole('textbox', { name: 'Sort number for deepseek-chat' })
  ).toHaveValue('9')
  await userEvent.click(
    screen.getByRole('tab', { name: 'Descriptions and badges' })
  )
  expect(screen.getByText('Keep metadata')).toBeVisible()
  expect(
    screen.getByRole('button', { name: 'Save descriptions and badges' })
  ).toBeEnabled()
  expect(screen.queryByText('Other page')).not.toBeInTheDocument()
})
