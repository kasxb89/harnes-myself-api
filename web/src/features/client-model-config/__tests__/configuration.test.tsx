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
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { ClientModelConfigPage } from '..'
import { clientModelConfigSchema } from '../lib/schema'
import type { ClientModelConfig, ClientModelOption } from '../types'

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
  },
]
const clients: QueryClient[] = []
let saved: ClientModelConfig

beforeEach(() => {
  saved = structuredClone(initial)
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/client-model-config') {
      return { data: { success: true, data: saved } }
    }
    if (url === '/api/client-model-config/models') {
      return { data: { success: true, data: { models } } }
    }
    throw new Error(`Unexpected API request: ${url}`)
  })
  vi.spyOn(api, 'put').mockImplementation(async (_url, body) => {
    saved = body as ClientModelConfig
    return { data: { success: true, data: saved } }
  })
})

afterEach(() => {
  clients.splice(0).forEach((client) => client.clear())
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
    screen.getByRole('combobox', { name: 'Default chat model' })
  ).toHaveValue('No default preference')
  expect(screen.getByText('No model order preference')).toBeVisible()
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

test('adding models removes duplicate candidates and keyboard and arrow controls change the saved order', async () => {
  const user = userEvent.setup()
  await setup()
  await selectOption('Add prioritized model', /deepseek-chat/)
  await selectOption('Add prioritized model', /gpt-chat/)
  const order = screen.getByRole('list', { name: 'Client model order' })
  expect(
    within(order).getByRole('button', { name: 'Move deepseek-chat up' })
  ).toBeDisabled()
  await user.click(
    within(order).getByRole('button', { name: 'Move gpt-chat up' })
  )
  within(order)
    .getByRole('button', { name: 'Drag deepseek-chat to reorder' })
    .focus()
  await user.keyboard('{ArrowUp}')
  await user.click(
    screen.getByRole('combobox', { name: 'Add prioritized model' })
  )
  expect(
    screen.queryByRole('option', { name: /deepseek-chat/ })
  ).not.toBeInTheDocument()
  await user.keyboard('{Escape}')
  await user.click(
    screen.getByRole('button', { name: 'Save client model configuration' })
  )
  await waitFor(() =>
    expect(saved.model_order).toEqual(['deepseek-chat', 'gpt-chat'])
  )
})

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
  await user.click(screen.getByRole('button', { name: 'Discard changes' }))
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
  await user.click(screen.getByRole('button', { name: 'Move gpt-chat up' }))
  await user.click(
    screen.getByRole('button', { name: 'Save client model configuration' })
  )
  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  expect(
    screen.getByRole('combobox', { name: 'Default chat model' })
  ).toBeDisabled()
  expect(
    screen.getByRole('button', { name: 'Move gpt-chat down' })
  ).toBeDisabled()
  expect(
    screen.getByRole('button', { name: 'Drag gpt-chat to reorder' })
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

test('empty catalog keeps empty preferences explicit and disables adding a model', async () => {
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
  expect(
    screen.getByRole('combobox', { name: 'Add prioritized model' })
  ).toBeDisabled()
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
