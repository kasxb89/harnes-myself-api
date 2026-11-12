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
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { ClientModelConfigPage } from '..'
import { MediaParametersForm } from '../components/media-parameters-form'
import { UnifiedModelForm } from '../components/unified-model-form'
import {
  readMediaParameters,
  nativeMediaFields,
  mediaParameterErrors,
  importMediaCombinations,
  updateMediaField,
  parameterSeed,
  serializeMediaParameters,
} from '../lib/media-parameters'
import {
  clientModelConfigFormSchema,
  clientModelConfigSchema,
  unifiedModelFormSchema,
} from '../lib/schema'
import type { ClientModelConfig, ClientModelOption } from '../types'
import type { UnifiedModelDefinition } from '../unified-api'

test('unified editor accepts a smaller default reservation and rejects defaults above the hard output cap', () => {
  const definition = {
    display_name: 'Split output',
    kind: 'text',
    enabled: true,
    source: 'Local capacity evidence',
    routes: [],
    parameter_json: '',
    cost_json: '[]',
    context_window: 1048576,
    max_output_tokens: 1048576,
  }
  expect(
    unifiedModelFormSchema.parse({
      ...definition,
      default_output_tokens: 131072,
    }).default_output_tokens
  ).toBe(131072)
  expect(
    unifiedModelFormSchema.safeParse({
      ...definition,
      default_output_tokens: 1048577,
    }).success
  ).toBe(false)
  expect(
    unifiedModelFormSchema.safeParse({
      ...definition,
      default_output_tokens: 0,
    }).success
  ).toBe(false)
  expect(unifiedModelFormSchema.safeParse(definition).success).toBe(true)
})

test('unified editor keeps unknown limits blank and requires the correct context/output pair before enabling text', () => {
  const draft = {
    display_name: 'Local test model',
    kind: 'text',
    enabled: false,
    source: 'Local synthetic declaration',
    routes: [],
    parameter_json: '',
    cost_json: '[]',
    context_window: Number.NaN,
    max_output_tokens: Number.NaN,
  }
  expect(unifiedModelFormSchema.safeParse(draft).success).toBe(true)
  expect(
    unifiedModelFormSchema.safeParse({ ...draft, enabled: true }).success
  ).toBe(false)
  expect(
    unifiedModelFormSchema.safeParse({
      ...draft,
      enabled: true,
      context_window: 32768,
    }).success
  ).toBe(false)
  expect(
    unifiedModelFormSchema.safeParse({
      ...draft,
      enabled: true,
      context_window: 32768,
      max_output_tokens: 8192,
    }).success
  ).toBe(true)
  expect(
    unifiedModelFormSchema.safeParse({
      ...draft,
      enabled: true,
      context_window: 8192,
      max_output_tokens: 32768,
    }).success
  ).toBe(false)
})

const initial: ClientModelConfig = {
  schema_version: 1,
  default_chat_model: '',
  model_order: [],
}
const models: ClientModelOption[] = [
  { id: 'deepseek-chat', display_name: 'DeepSeek Chat', chat_eligible: true },
  { id: 'gpt-chat', display_name: 'GPT Chat', chat_eligible: true },
  {
    id: 'yunshu-internal-gpt-image-2',
    display_name: 'Image model',
    chat_eligible: false,
    image_eligible: true,
  },
]
const clients: QueryClient[] = []
let mediaSaved = {
  schema_version: 1 as const,
  default_image_model: '',
  default_video_model: '',
}
let saved: ClientModelConfig

beforeEach(() => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'root', role: ROLE.SUPER_ADMIN })
  saved = structuredClone(initial)
  mediaSaved = {
    schema_version: 1,
    default_image_model: '',
    default_video_model: '',
  }
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/status') {
      return { data: { success: true, data: { usd_exchange_rate: 7.3 } } }
    }
    if (url === '/api/option/model_pricing') {
      return { data: { success: true, data: { entries: [] } } }
    }
    if (url === '/api/client-model-config/media-defaults') {
      return { data: { success: true, data: mediaSaved } }
    }
    if (url === '/api/client-model-config') {
      return { data: { success: true, data: saved } }
    }
    if (url === '/api/client-model-config/presentation') {
      return {
        data: { success: true, data: { schema_version: 1, models: {} } },
      }
    }
    if (url === '/api/client-model-config/models') {
      return { data: { success: true, data: { models } } }
    }
    throw new Error(`Unexpected API request: ${url}`)
  })
  vi.spyOn(api, 'put').mockImplementation(async (_url, body) => {
    if (_url === '/api/client-model-config/media-defaults') {
      mediaSaved = body as typeof mediaSaved
      return { data: { success: true, data: mediaSaved } }
    }
    saved = body as ClientModelConfig
    return { data: { success: true, data: saved } }
  })
})

afterEach(() => {
  clients.splice(0).forEach((client) => client.clear())
})

test('unified form saves the default independently and blocks a reservation above the maximum', async () => {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  const definition: UnifiedModelDefinition = {
    model_name: 'split-output',
    display_name: 'Split output',
    kind: 'text',
    enabled: true,
    source: 'Local synthetic output limits',
    routes: [],
    pricing: {},
    context_window: 1048576,
    max_output_tokens: 1048576,
    default_output_tokens: 131072,
  }
  const save = vi.fn(async (_definition: UnifiedModelDefinition) => {})
  render(
    <QueryClientProvider client={client}>
      <UnifiedModelForm
        entry={{
          model_name: definition.model_name,
          base_version: 'fixture',
          definition,
        }}
        channels={[]}
        pending={false}
        onSave={save}
        onDirtyChange={vi.fn()}
      />
    </QueryClientProvider>
  )
  const reserve = screen.getByLabelText('Default output reservation')
  expect(reserve).toHaveValue(131072)
  fireEvent.change(reserve, { target: { value: '1048577' } })
  await userEvent.click(
    screen.getByRole('button', { name: 'Validate and save draft' })
  )
  expect(
    await screen.findByText(
      'Default output tokens cannot exceed maximum output tokens'
    )
  ).toBeVisible()
  expect(save).not.toHaveBeenCalled()
  fireEvent.change(reserve, { target: { value: '65536' } })
  await userEvent.click(
    screen.getByRole('button', { name: 'Validate and save draft' })
  )
  await waitFor(() => expect(save).toHaveBeenCalledOnce())
  expect(save.mock.calls[0]?.[0]).toMatchObject({
    context_window: 1048576,
    max_output_tokens: 1048576,
    default_output_tokens: 65536,
    pricing: {},
  })
})

async function setup(): Promise<QueryClient> {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  clients.push(client)
  const root = createRootRoute({ component: ClientModelConfigPage })
  const router = createRouter({
    routeTree: root,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await router.load()
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  await screen.findByRole('combobox', { name: 'Default chat model' })
  return client
}

async function selectOption(
  label: string,
  name: string | RegExp
): Promise<void> {
  const user = userEvent.setup()
  await user.click(screen.getByRole('combobox', { name: label }))
  await user.click(await screen.findByRole('option', { name }))
}

test('empty preferences show the catalog fallback and save stays disabled until an edit', async () => {
  await setup()
  expect(
    screen.queryByRole('tab', { name: 'Context and output limits' })
  ).toBeNull()
  expect(
    screen.getAllByRole('tab', { name: 'Models, prices and parameters' })
  ).toHaveLength(1)
  expect(
    screen.getByRole('combobox', { name: 'Default chat model' })
  ).toHaveValue('No default preference')
  expect(
    screen.getByRole('table', { name: 'Client model order' })
  ).toBeVisible()
  expect(
    screen.getAllByRole('textbox', { name: /^Sort number for / })
  ).toHaveLength(3)
  expect(
    screen.getByRole('textbox', { name: 'Sort number for deepseek-chat' })
  ).toHaveValue('')
  expect(
    screen.getByRole('button', { name: 'Save client model configuration' })
  ).toBeDisabled()
  expect(api.put).not.toHaveBeenCalled()
})

test('chat default selection excludes image models and saves one complete precise-ID object', async () => {
  const user = userEvent.setup()
  await setup()
  await user.click(screen.getByRole('combobox', { name: 'Default chat model' }))
  expect(
    screen.queryByRole('option', { name: /yunshu-internal/ })
  ).not.toBeInTheDocument()
  await user.click(await screen.findByRole('option', { name: /deepseek-chat/ }))
  await user.click(
    screen.getByRole('button', { name: 'Save client model configuration' })
  )
  await waitFor(() =>
    expect(api.put).toHaveBeenCalledWith('/api/client-model-config', {
      schema_version: 1,
      default_chat_model: 'deepseek-chat',
      model_order: [],
    })
  )
  expect(
    screen.getByRole('button', { name: 'Save client model configuration' })
  ).toBeDisabled()
})

test('all catalog models accept sort numbers and save a stable precise-ID order without moving focused rows', async () => {
  const user = userEvent.setup()
  await setup()
  const table = screen.getByRole('table', { name: 'Client model order' })
  const deepseek = within(table).getByRole('textbox', {
    name: 'Sort number for deepseek-chat',
  })
  const gpt = within(table).getByRole('textbox', {
    name: 'Sort number for gpt-chat',
  })
  const image = within(table).getByRole('textbox', {
    name: 'Sort number for yunshu-internal-gpt-image-2',
  })
  await user.type(gpt, '20')
  await user.type(deepseek, '20')
  await user.type(image, '10')
  expect(image).toHaveFocus()
  expect(
    within(table)
      .getAllByRole('textbox')
      .map((input) => input.getAttribute('aria-label'))
  ).toEqual([
    'Sort number for deepseek-chat',
    'Sort number for gpt-chat',
    'Sort number for yunshu-internal-gpt-image-2',
  ])
  await user.click(
    screen.getByRole('button', { name: 'Save client model configuration' })
  )
  await waitFor(() =>
    expect(saved).toEqual({
      ...initial,
      model_order: ['yunshu-internal-gpt-image-2', 'deepseek-chat', 'gpt-chat'],
    })
  )
  expect(image).toHaveValue('1')
  expect(deepseek).toHaveValue('2')
  expect(gpt).toHaveValue('3')
})

test('blank numbers stay after ranked models and clearing a rank keeps its catalog row', async () => {
  const user = userEvent.setup()
  await setup()
  const input = screen.getByRole('textbox', {
    name: 'Sort number for gpt-chat',
  })
  await user.type(input, '9')
  await user.click(
    screen.getByRole('button', { name: 'Save client model configuration' })
  )
  await waitFor(() => expect(saved.model_order).toEqual(['gpt-chat']))
  await user.clear(input)
  await user.click(
    screen.getByRole('button', { name: 'Save client model configuration' })
  )
  await waitFor(() => expect(saved.model_order).toEqual([]))
  expect(input).toBeVisible()
})

test.each(['0', '-1', '1.5', '1000000', '1e2', 'abc'])(
  'invalid sort number %s remains editable and blocks saving',
  async (value) => {
    const user = userEvent.setup()
    await setup()
    const input = screen.getByRole('textbox', {
      name: 'Sort number for gpt-chat',
    })
    await user.type(input, value)
    expect(input).toHaveValue(value)
    expect(input).toHaveAttribute('aria-invalid', 'true')
    expect(
      screen.getByText('Enter a whole number from 1 to 999999, or leave blank.')
    ).toBeVisible()
    expect(
      screen.getByRole('button', { name: 'Save client model configuration' })
    ).toBeDisabled()
    expect(
      screen.getByRole('button', { name: 'Discard changes' })
    ).toBeEnabled()
    await user.clear(input)
    await user.type(input, '2')
    expect(input).toHaveAttribute('aria-invalid', 'false')
    await user.click(
      screen.getByRole('button', { name: 'Save client model configuration' })
    )
    await waitFor(() => expect(saved.model_order).toEqual(['gpt-chat']))
  }
)

test('clearing order and default saves empty preferences without changing the catalog', async () => {
  const user = userEvent.setup()
  saved = {
    schema_version: 1,
    default_chat_model: 'deepseek-chat',
    model_order: ['gpt-chat'],
  }
  await setup()
  await selectOption('Default chat model', 'No default preference')
  await user.click(screen.getByRole('button', { name: 'Use catalog order' }))
  await user.click(
    screen.getByRole('button', { name: 'Save client model configuration' })
  )
  await waitFor(() => expect(saved).toEqual(initial))
  expect(models).toHaveLength(3)
})

test('failed save keeps the edited selection and does not report success or reset the draft', async () => {
  const user = userEvent.setup()
  vi.mocked(api.put).mockResolvedValueOnce({
    data: {
      success: false,
      message: 'Failed to save client model configuration',
    },
  })
  await setup()
  await selectOption('Default chat model', /gpt-chat/)
  const rank = screen.getByRole('textbox', { name: 'Sort number for gpt-chat' })
  await user.type(rank, '50')
  await user.click(
    screen.getByRole('button', { name: 'Save client model configuration' })
  )
  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Save client model configuration' })
    ).toBeEnabled()
  )
  expect(
    screen.getByRole('combobox', { name: 'Default chat model' })
  ).toHaveValue('gpt-chat')
  expect(rank).toHaveValue('50')
  await user.click(screen.getByRole('button', { name: 'Discard changes' }))
  expect(rank).toHaveValue('')
  expect(
    screen.getByRole('combobox', { name: 'Default chat model' })
  ).toHaveValue('No default preference')
})

test('unavailable saved IDs remain visible and block save until explicitly removed', async () => {
  const user = userEvent.setup()
  saved = {
    schema_version: 1,
    default_chat_model: 'retired-model',
    model_order: ['retired-model'],
  }
  await setup()
  expect(
    screen.getByRole('combobox', { name: 'Default chat model' })
  ).toHaveValue('retired-model')
  expect(screen.getByText('Unavailable')).toBeVisible()
  expect(
    screen.getByRole('button', { name: 'Save client model configuration' })
  ).toBeDisabled()
  await user.click(
    screen.getByRole('button', { name: 'Remove retired-model from priority' })
  )
  await selectOption('Default chat model', 'No default preference')
  await user.click(
    screen.getByRole('button', { name: 'Save client model configuration' })
  )
  await waitFor(() => expect(saved).toEqual(initial))
})

test('catalog failure displays retry and never presents an editable empty catalog', async () => {
  vi.mocked(api.get)
    .mockResolvedValueOnce({ data: { success: true, data: saved } })
    .mockRejectedValueOnce(new Error('offline'))
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  const root = createRootRoute({ component: ClientModelConfigPage })
  const router = createRouter({
    routeTree: root,
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  await router.load()
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  expect(
    await screen.findByText('Failed to load client model configuration')
  ).toBeVisible()
  expect(screen.queryByRole('combobox')).not.toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }))
  expect(
    await screen.findByRole('combobox', { name: 'Default chat model' })
  ).toBeEnabled()
})

test('configuration validation rejects duplicate IDs and bounds precise IDs by UTF-8 bytes', () => {
  expect(
    clientModelConfigSchema.safeParse({
      ...initial,
      model_order: ['deepseek-chat', 'deepseek-chat'],
    }).success
  ).toBe(false)
  expect(
    clientModelConfigSchema.safeParse({
      ...initial,
      default_chat_model: '模'.repeat(86),
    }).success
  ).toBe(false)
  expect(
    clientModelConfigSchema.safeParse({
      ...initial,
      default_chat_model: '模'.repeat(85),
    }).success
  ).toBe(true)
  expect(
    clientModelConfigSchema.safeParse({
      ...initial,
      model_order: [],
      unexpected: true,
    }).success
  ).toBe(false)
})

test('refetch updates pristine values while preserving an unsaved draft', async () => {
  const client = await setup()
  saved = {
    schema_version: 1,
    default_chat_model: 'deepseek-chat',
    model_order: [],
  }
  await act(() =>
    client.invalidateQueries({ queryKey: ['client-model-config'] })
  )
  await waitFor(() =>
    expect(
      screen.getByRole('combobox', { name: 'Default chat model' })
    ).toHaveValue('deepseek-chat')
  )
  await selectOption('Default chat model', /gpt-chat/)
  saved = { ...initial }
  await act(() =>
    client.invalidateQueries({ queryKey: ['client-model-config'] })
  )
  expect(
    screen.getByRole('combobox', { name: 'Default chat model' })
  ).toHaveValue('gpt-chat')
})

test('pending save disables model selection and order actions until the response arrives', async () => {
  const user = userEvent.setup()
  saved = {
    schema_version: 1,
    default_chat_model: '',
    model_order: ['deepseek-chat', 'gpt-chat'],
  }
  let finish: (result: {
    data: { success: boolean; data: ClientModelConfig }
  }) => void = () => undefined
  vi.mocked(api.put).mockImplementationOnce(
    () =>
      new Promise((resolve) => {
        finish = resolve
      })
  )
  await setup()
  const rank = screen.getByRole('textbox', { name: 'Sort number for gpt-chat' })
  await user.clear(rank)
  await user.type(rank, '1')
  fireEvent.change(
    screen.getByRole('textbox', { name: 'Sort number for deepseek-chat' }),
    { target: { value: '2' } }
  )
  await user.click(
    screen.getByRole('button', { name: 'Save client model configuration' })
  )
  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  expect(
    screen.getByRole('combobox', { name: 'Default chat model' })
  ).toBeDisabled()
  expect(rank).toBeDisabled()
  expect(
    screen.getByRole('button', { name: 'Use catalog order' })
  ).toBeDisabled()
  await act(async () =>
    finish({
      data: {
        success: true,
        data: { ...initial, model_order: ['gpt-chat', 'deepseek-chat'] },
      },
    })
  )
  expect(
    screen.getByRole('combobox', { name: 'Default chat model' })
  ).toBeEnabled()
})

test('invalid numeric draft survives a successful background refetch until explicitly discarded', async () => {
  const user = userEvent.setup()
  const client = await setup()
  const rank = screen.getByRole('textbox', { name: 'Sort number for gpt-chat' })
  await user.type(rank, '1e2')
  saved = { ...initial, model_order: ['deepseek-chat'] }
  await act(() =>
    client.invalidateQueries({ queryKey: ['client-model-config'] })
  )
  expect(rank).toHaveValue('1e2')
  expect(rank).toHaveAttribute('aria-invalid', 'true')
  expect(screen.getByRole('button', { name: 'Discard changes' })).toBeEnabled()
  await user.click(screen.getByRole('button', { name: 'Discard changes' }))
  await waitFor(() => expect(rank).toHaveValue(''))
  expect(
    screen.getByRole('textbox', { name: 'Sort number for deepseek-chat' })
  ).toHaveValue('1')
})

test('256 prioritized models block new numbers until one is cleared', async () => {
  const user = userEvent.setup()
  const catalog = Array.from({ length: 257 }, (_, index) => ({
    id: `catalog-${index + 1}`,
    display_name: `Catalog ${index + 1}`,
    chat_eligible: true,
  }))
  saved = {
    ...initial,
    model_order: catalog.slice(0, 256).map((model) => model.id),
  }
  vi.mocked(api.get).mockImplementation(async (url) => ({
    data: {
      success: true,
      data: url === '/api/client-model-config' ? saved : { models: catalog },
    },
  }))
  await setup()
  const extra = screen.getByRole('textbox', {
    name: 'Sort number for catalog-257',
  })
  expect(extra).toBeDisabled()
  expect(screen.getByText('You can prioritize up to 256 models.')).toBeVisible()
  await user.clear(
    screen.getByRole('textbox', { name: 'Sort number for catalog-1' })
  )
  expect(extra).toBeEnabled()
  await user.type(extra, '1')
  expect(
    screen.getByRole('textbox', { name: 'Sort number for catalog-1' })
  ).toBeDisabled()
  await user.click(
    screen.getByRole('button', { name: 'Save client model configuration' })
  )
  await waitFor(() => expect(saved.model_order).toHaveLength(256))
  expect(saved.model_order[0]).toBe('catalog-257')
  expect(saved.model_order).not.toContain('catalog-1')
  expect(
    clientModelConfigFormSchema.safeParse({
      default_chat_model: '',
      model_ranks: catalog.map((model) => ({ id: model.id, rank: '1' })),
    }).success
  ).toBe(false)
})

test('long model IDs wrap and numeric errors remain associated with their input', async () => {
  const id = `model-${'x'.repeat(249)}`
  vi.mocked(api.get).mockImplementation(async (url) => ({
    data: {
      success: true,
      data:
        url === '/api/client-model-config'
          ? saved
          : { models: [{ id, display_name: id, chat_eligible: true }] },
    },
  }))
  await setup()
  const input = screen.getByRole('textbox', { name: `Sort number for ${id}` })
  fireEvent.change(input, { target: { value: '1000000' } })
  expect(screen.getByText(id)).toHaveClass('break-all')
  expect(input).toHaveAccessibleDescription(
    'Enter a whole number from 1 to 999999, or leave blank.'
  )
  expect(screen.getByRole('table', { name: 'Client model order' })).toHaveClass(
    'table-fixed'
  )
})

test('failed background refetch leaves an existing dirty draft editable', async () => {
  const client = await setup()
  await selectOption('Default chat model', /gpt-chat/)
  vi.mocked(api.get).mockRejectedValueOnce(new Error('offline'))
  await act(() =>
    client.invalidateQueries({ queryKey: ['client-model-config'] })
  )
  expect(
    screen.getByRole('combobox', { name: 'Default chat model' })
  ).toHaveValue('gpt-chat')
  expect(
    screen.getByRole('button', { name: 'Save client model configuration' })
  ).toBeEnabled()
})

test('empty catalog keeps empty preferences explicit and has no numeric rows', async () => {
  vi.mocked(api.get).mockImplementation(async (url) => {
    if (url === '/api/client-model-config') {
      return { data: { success: true, data: saved } }
    }
    return { data: { success: true, data: { models: [] } } }
  })
  await setup()
  expect(
    screen.getByRole('combobox', { name: 'Default chat model' })
  ).toHaveValue('No default preference')
  expect(screen.getByText('No available models')).toBeVisible()
  expect(
    screen.queryByRole('textbox', { name: /^Sort number for / })
  ).not.toBeInTheDocument()
  expect(
    screen.getByRole('button', { name: 'Save client model configuration' })
  ).toBeDisabled()
})

test('configuration actions can wrap within the viewport for long translated labels', async () => {
  await setup()
  const actions = screen.getByRole('group', {
    name: 'Client model configuration',
  })
  expect(actions).toHaveClass('flex-wrap', 'max-w-[calc(100vw-2rem)]')
  expect(
    within(actions).getByRole('button', {
      name: 'Save client model configuration',
    })
  ).toBeVisible()
  expect(
    within(actions).getByRole('button', { name: 'Discard changes' })
  ).toBeVisible()
})

test('read-only model configuration disables fields and exposes no save or description edit action', async () => {
  useAuthStore.getState().auth.setUser({
    id: 2,
    username: 'reader',
    role: ROLE.ADMIN,
    permissions: {
      admin_permissions: {
        client_model_config: { read: true, write: false },
      },
    },
  })
  const user = userEvent.setup()
  await setup()
  expect(
    screen.getByRole('combobox', { name: 'Default chat model' })
  ).toBeDisabled()
  expect(
    screen.queryByRole('button', { name: 'Save client model configuration' })
  ).not.toBeInTheDocument()
  await user.click(
    screen.getByRole('tab', { name: 'Image and video defaults' })
  )
  expect(
    await screen.findByRole('combobox', { name: 'Default image model' })
  ).toBeDisabled()
  expect(
    screen.getByRole('combobox', { name: 'Default video model' })
  ).toBeDisabled()
  await user.click(screen.getByRole('tab', { name: 'Descriptions and badges' }))
  expect(
    await screen.findByRole('searchbox', { name: 'Search model IDs' })
  ).toBeEnabled()
  expect(
    screen.queryByRole('button', { name: 'Save descriptions and badges' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: /Edit presentation for/ })
  ).not.toBeInTheDocument()
  expect(api.put).not.toHaveBeenCalled()
})

test('revoking model configuration access removes existing cached forms immediately', async () => {
  await setup()
  act(() =>
    useAuthStore.getState().auth.setUser({
      id: 1,
      username: 'operator',
      role: ROLE.ADMIN,
      permissions: { admin_permissions: {} },
    })
  )
  expect(screen.getByText('Access denied')).toBeVisible()
  expect(
    screen.queryByRole('combobox', { name: 'Default chat model' })
  ).not.toBeInTheDocument()
})

test('media defaults only list declared modalities and save separately from chat preferences', async () => {
  const priorGet = vi.mocked(api.get).getMockImplementation()
  if (!priorGet) throw new Error('API fixture missing')
  vi.mocked(api.get).mockImplementation(async (url, config) =>
    url === '/api/client-model-config/models'
      ? {
          data: {
            success: true,
            data: {
              models: [
                ...models,
                {
                  id: 'MiniMax-H3',
                  display_name: 'H3 video',
                  chat_eligible: false,
                  video_eligible: true,
                },
              ],
            },
          },
        }
      : priorGet(url, config)
  )
  await setup()
  const user = userEvent.setup()
  await user.click(
    screen.getByRole('tab', { name: 'Image and video defaults' })
  )
  await screen.findByRole('combobox', { name: 'Default image model' })
  await selectOption('Default image model', /Image model/)
  await selectOption('Default video model', /H3 video/)
  const buttons = screen.getAllByRole('button', { name: /Save/ })
  const saveButton = buttons.find((b) => !b.hasAttribute('disabled'))
  expect(saveButton).toBeDefined()
  if (!saveButton) throw new Error('save unavailable')
  await user.click(saveButton)
  await waitFor(() =>
    expect(api.put).toHaveBeenCalledWith(
      '/api/client-model-config/media-defaults',
      {
        schema_version: 1,
        default_image_model: 'yunshu-internal-gpt-image-2',
        default_video_model: 'MiniMax-H3',
      }
    )
  )
  expect(saved).toEqual(initial)
})

test('a failed media preference save retains the selected draft for retry', async () => {
  await setup()
  const user = userEvent.setup()
  await user.click(
    screen.getByRole('tab', { name: 'Image and video defaults' })
  )
  await screen.findByRole('combobox', { name: 'Default image model' })
  await selectOption('Default image model', /Image model/)
  vi.mocked(api.put).mockRejectedValueOnce(new Error('fixture save failure'))
  const saveButton = screen
    .getAllByRole('button', { name: /Save/ })
    .find((b) => !b.hasAttribute('disabled'))
  if (!saveButton) throw new Error('save unavailable')
  await user.click(saveButton)
  await waitFor(() => expect(saveButton).toBeEnabled())
  expect(
    screen.getByRole('combobox', { name: 'Default image model' })
  ).toHaveValue('Image model')
  expect(mediaSaved.default_image_model).toBe('')
})

test('empty platform defaults explain catalog fallbacks and the H3 resolution without pretending to know personal choices', async () => {
  await setup()
  const summary = await screen.findByRole('region', {
    name: 'Saved platform defaults',
  })
  expect(
    within(summary).getByText(
      'No platform chat model is set. Clients following platform policy use the first available chat model in the configured order.'
    )
  ).toBeVisible()
  expect(
    within(summary).getByText(
      'No platform image model is set. Use the first model in the account image catalog.'
    )
  ).toBeVisible()
  expect(
    within(summary).getByText(
      'No platform video model is set. Use mt-MiniMax-H3, with 768P when no resolution is specified.'
    )
  ).toBeVisible()
  expect(
    within(summary).getByText(
      'The initial built-in chat model is deepseek-v4-flash; the account catalog and access still determine availability.'
    )
  ).toBeVisible()
  expect(api.put).not.toHaveBeenCalled()
})

test('platform defaults summary shows saved values until a draft is successfully saved', async () => {
  saved.default_chat_model = 'deepseek-chat'
  mediaSaved.default_image_model = 'yunshu-internal-gpt-image-2'
  await setup()
  const summary = await screen.findByRole('region', {
    name: 'Saved platform defaults',
  })
  expect(within(summary).getByText('deepseek-chat')).toBeVisible()
  expect(within(summary).getByText('yunshu-internal-gpt-image-2')).toBeVisible()
  await selectOption('Default chat model', /gpt-chat/)
  expect(within(summary).getByText('deepseek-chat')).toBeVisible()
  expect(within(summary).queryByText('gpt-chat')).not.toBeInTheDocument()
  await userEvent
    .setup()
    .click(screen.getByRole('tab', { name: 'Image and video defaults' }))
  expect(
    screen.queryByText(
      'No platform image model is set. Use the first model in the account image catalog.'
    )
  ).not.toBeInTheDocument()
})

const nativeMediaFixture = {
  type: 'object',
  properties: {
    ratio: { type: 'string', enum: ['16:9', '9:16'], default: '16:9' },
    seconds: { type: 'integer', minimum: 4, maximum: 30, default: 5 },
    generate_audio: { type: 'boolean', default: false },
  },
  oneOf: [
    {
      properties: {
        resolution: { enum: ['768P'] },
        seconds: { minimum: 4, maximum: 30 },
      },
    },
    {
      properties: {
        resolution: { enum: ['2K'] },
        seconds: { minimum: 4, maximum: 15 },
      },
    },
  ],
  'x-reference-limits': { images: 9, videos: 3, audios: 3 },
  'x-reference-frame-max-images': 2,
  'x-client-concurrency': { max_parallelism: 4, atomic_admission: true },
  'x-task-recovery': { durable: true },
}

function MediaFormHarness(props: {
  initial?: string
  native?: Record<string, unknown>
  onChange: (value: string) => void
  disabled?: boolean
}) {
  const [value, setValue] = useState(props.initial ?? '')
  return (
    <MediaParametersForm
      value={value}
      nativeSchema={props.native ?? nativeMediaFixture}
      disabled={props.disabled}
      onChange={(next) => {
        setValue(next)
        props.onChange(next)
      }}
    />
  )
}

test('media form shows inherited values without JSON, mutating the adapter, or dirtying an unchanged draft', () => {
  const original = JSON.stringify(nativeMediaFixture)
  const change = vi.fn()
  render(<MediaFormHarness onChange={change} />)
  expect(
    screen.queryByRole('textbox', { name: 'Generation parameter schema' })
  ).toBeNull()
  expect(
    screen.getByRole('textbox', {
      name: 'Maximum for Video duration (seconds)',
    })
  ).toHaveValue('30')
  expect(
    screen.getByRole('textbox', {
      name: 'Maximum for Video duration (seconds)',
    })
  ).toBeDisabled()
  expect(
    screen.getByRole('table', { name: 'Generation specification combinations' })
  ).toHaveTextContent('2K')
  expect(
    screen.getByRole('table', { name: 'Generation specification combinations' })
  ).toHaveTextContent('4–15')
  expect(change).not.toHaveBeenCalled()
  expect(JSON.stringify(nativeMediaFixture)).toBe(original)
})

test('media form edits duration, reference count and audio defaults as controls without copying host declarations', async () => {
  const change = vi.fn()
  render(<MediaFormHarness onChange={change} />)
  await selectOption('Settings for Video duration (seconds)', 'Customize')
  fireEvent.change(
    screen.getByRole('textbox', {
      name: 'Maximum for Video duration (seconds)',
    }),
    { target: { value: '20' } }
  )
  await selectOption('Settings for Reference images', 'Customize')
  fireEvent.change(
    screen.getByRole('textbox', { name: 'Maximum for Reference images' }),
    { target: { value: '5' } }
  )
  await selectOption('Settings for Generate audio', 'Customize')
  await selectOption('Default value for Generate audio', 'Yes')
  const saved = JSON.parse(change.mock.lastCall?.[0] ?? '{}')
  expect(saved.properties.seconds).toMatchObject({
    type: 'integer',
    minimum: 4,
    maximum: 20,
    default: 5,
  })
  expect(saved.properties.reference_images).toEqual({
    type: 'array',
    items: { type: 'string' },
    maxItems: 5,
  })
  expect(saved.properties.generate_audio).toEqual({
    type: 'boolean',
    default: true,
  })
  expect(saved).not.toHaveProperty('oneOf')
  expect(saved).not.toHaveProperty('x-client-concurrency')
  expect(saved).not.toHaveProperty('x-task-recovery')
  expect(saved.properties).not.toHaveProperty('ratio')
})

test('media form keeps unedited combinations and fields when a single allowed parameter value changes', async () => {
  const initial = {
    type: 'object',
    properties: {
      ratio: { type: 'string', enum: ['16:9', '9:16'], default: '16:9' },
      seconds: { type: 'integer', minimum: 4, maximum: 30, default: 5 },
      resolution: { type: 'string', enum: ['768P', '2K'] },
    },
    oneOf: [
      {
        properties: {
          resolution: { type: 'string', const: '2K' },
          seconds: { type: 'integer', maximum: 15 },
        },
      },
    ],
  }
  const change = vi.fn()
  render(
    <MediaFormHarness initial={JSON.stringify(initial)} onChange={change} />
  )
  fireEvent.change(
    within(
      screen.getByRole('group', { name: 'General generation parameters' })
    ).getByRole('textbox', {
      name: 'Maximum for Video duration (seconds)',
    }),
    { target: { value: '25' } }
  )
  const saved = JSON.parse(change.mock.lastCall?.[0] ?? '{}')
  expect(saved.oneOf).toEqual(initial.oneOf)
  expect(saved.properties.ratio).toEqual(initial.properties.ratio)
  expect(saved.properties.resolution).toEqual(initial.properties.resolution)
  expect(saved.properties.seconds.maximum).toBe(25)
})

test('media combinations import native constraints with declared types and edit only the selected branch', async () => {
  const change = vi.fn()
  render(<MediaFormHarness onChange={change} />)
  await selectOption('Specification combination settings', 'Customize')
  await selectOption('Edit specification combination', 'Combination 2')
  const combo = screen.getByRole('combobox', {
    name: 'Edit specification combination',
  })
  expect(combo).toHaveValue('Combination 2')
  const maxima = screen.getAllByRole('textbox', {
    name: 'Maximum for Video duration (seconds)',
  })
  const maximum = maxima.at(-1)
  if (!maximum) throw new Error('Expected a visible combination duration field')
  fireEvent.change(maximum, { target: { value: '12' } })
  const saved = JSON.parse(change.mock.lastCall?.[0] ?? '{}')
  expect(saved.oneOf[0].properties.seconds.maximum).toBe(30)
  expect(saved.oneOf[1].properties.seconds.maximum).toBe(12)
  expect(saved.oneOf[1].properties.resolution).toEqual({
    type: 'string',
    enum: ['2K'],
  })
  expect(saved.properties.resolution).toEqual({ type: 'string' })
})

test('media form reports inverted limits and invalid defaults next to the parameter while retaining values', () => {
  const source = JSON.stringify({
    properties: {
      seconds: { type: 'integer', minimum: 20, maximum: 10, default: 30 },
    },
  })
  const change = vi.fn()
  render(<MediaFormHarness initial={source} onChange={change} />)
  expect(
    screen
      .getAllByRole('alert')
      .some((alert) =>
        alert.textContent?.includes('The minimum cannot exceed the maximum.')
      )
  ).toBe(true)
  expect(
    screen.getByRole('textbox', {
      name: 'Minimum for Video duration (seconds)',
    })
  ).toHaveValue('20')
  expect(change).not.toHaveBeenCalled()
})

test('media form pending state disables parameter and combination controls', () => {
  render(<MediaFormHarness onChange={vi.fn()} disabled />)
  expect(
    screen.getByRole('combobox', { name: 'Settings for Aspect ratio' })
  ).toBeDisabled()
  expect(
    screen.getByRole('combobox', { name: 'Specification combination settings' })
  ).toBeDisabled()
})

test('media form preserves unsupported native combination restrictions and never replaces them with broad guesses', () => {
  const native = readMediaParameters({
    ...nativeMediaFixture,
    oneOf: [
      { properties: { seconds: { minimum: 4, maximum: 30, multipleOf: 2 } } },
    ],
  })
  expect(native).not.toBeNull()
  if (!native) throw new Error('Expected native declaration')
  expect(importMediaCombinations({}, native)).toBeNull()
  expect(nativeMediaFields(native).seconds).toMatchObject({
    minimum: 4,
    maximum: 30,
  })
  expect(
    mediaParameterErrors({
      properties: { seconds: { type: 'integer', minimum: 31, maximum: 30 } },
    })
  ).toContain('The minimum cannot exceed the maximum.')
  expect(
    serializeMediaParameters(updateMediaField({}, 'seconds', undefined))
  ).toBe('')
  expect(parameterSeed('reference_images')).toEqual({
    type: 'array',
    items: { type: 'string' },
  })
})

test('image parameters select size and output format defaults while preserving count limits', async () => {
  const initial = {
    type: 'object',
    properties: {
      size: { type: 'string', enum: ['1K', '2K'], default: '1K' },
      output_format: { type: 'string', enum: ['png', 'webp'], default: 'png' },
      n: { type: 'integer', minimum: 1, maximum: 4, default: 1 },
    },
  }
  const change = vi.fn()
  render(
    <MediaFormHarness
      initial={JSON.stringify(initial)}
      native={{}}
      onChange={change}
    />
  )
  await selectOption('Default value for Image size', '2K')
  await selectOption('Default value for Output format', 'webp')
  const saved = JSON.parse(change.mock.lastCall?.[0] ?? '{}')
  expect(saved.properties.size.default).toBe('2K')
  expect(saved.properties.output_format.default).toBe('webp')
  expect(saved.properties.n).toEqual(initial.properties.n)
})

test('a newly declared parameter leaves unpublished native limits blank rather than guessing them', async () => {
  const change = vi.fn()
  render(<MediaFormHarness native={{}} onChange={change} />)
  await selectOption('Add generation parameter', 'Image count (n)')
  expect(
    screen.getByRole('textbox', { name: 'Minimum for Image count' })
  ).toHaveValue('')
  expect(
    screen.getByRole('textbox', { name: 'Maximum for Image count' })
  ).toHaveValue('')
  expect(JSON.parse(change.mock.lastCall?.[0] ?? '{}').properties.n).toEqual({
    type: 'integer',
  })
})
