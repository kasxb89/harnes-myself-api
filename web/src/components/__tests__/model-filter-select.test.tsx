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
import userEvent from '@testing-library/user-event'
import { useState, type ComponentProps } from 'react'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { ModelFilterSelect } from '@/components/model-filter-select'
import { Button } from '@/components/ui/button'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'

const clients: QueryClient[] = []
beforeEach(() => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 7, username: 'reader', role: 1, group: 'default' })
})
afterEach(() => {
  cleanup()
  clients.splice(0).forEach((client) => client.clear())
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
})
function setup(props: Partial<ComponentProps<typeof ModelFilterSelect>> = {}) {
  const change = vi.fn()
  const apply = vi.fn()
  function Fixture() {
    const [value, setValue] = useState(props.value ?? '')
    return (
      <form
        onSubmit={(event) => {
          event.preventDefault()
          apply(value)
        }}
      >
        <ModelFilterSelect
          {...props}
          value={value}
          onValueChange={(next) => {
            setValue(next)
            change(next)
          }}
        />
        <Button type='submit'>Apply</Button>
      </form>
    )
  }
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <Fixture />
    </QueryClientProvider>
  )
  return { change, apply }
}
it('offers current user models, historical report models and a retired URL selection exactly once', async () => {
  const get = vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: ['current-model', 'shared-model', ''] },
  })
  setup({ models: ['history-model', 'shared-model'], value: 'retired-model' })
  await waitFor(() =>
    expect(screen.queryByText('Loading...')).not.toBeInTheDocument()
  )
  const user = userEvent.setup()
  await user.click(screen.getByRole('combobox', { name: 'Model Name' }))
  for (const model of [
    'All models',
    'current-model',
    'history-model',
    'shared-model',
    'retired-model',
  ]) {
    expect(screen.getAllByRole('option', { name: model })).toHaveLength(1)
  }
  expect(screen.getByRole('option', { name: 'retired-model' })).toHaveAttribute(
    'aria-selected',
    'true'
  )
  expect(get).toHaveBeenCalledExactlyOnceWith('/api/user/models')
})
it('searches without applying arbitrary text, selects by keyboard without submitting and explicitly clears to All models', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: ['exact-model', 'exact-model-extended'] },
  })
  const { change, apply } = setup()
  const user = userEvent.setup()
  const input = screen.getByRole('combobox', { name: 'Model Name' })
  await user.click(input)
  await screen.findByRole('option', { name: 'exact-model' })
  await user.type(input, 'missing')
  expect(screen.getByText('No matching models')).toBeVisible()
  expect(change).not.toHaveBeenCalled()
  await user.keyboard('{Enter}')
  expect(apply).not.toHaveBeenCalled()
  await user.keyboard('{Escape}')
  expect(input).toHaveValue('All models')
  await user.click(input)
  await user.type(input, 'exact-model')
  await user.keyboard('{ArrowDown}{Enter}')
  expect(input).toHaveValue('exact-model')
  expect(change).toHaveBeenLastCalledWith('exact-model')
  expect(apply).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Apply' }))
  expect(apply).toHaveBeenLastCalledWith('exact-model')
  await user.click(input)
  await user.click(screen.getByRole('option', { name: 'All models' }))
  expect(change).toHaveBeenLastCalledWith('')
  expect(input).toHaveValue('All models')
})
it('keeps history usable while loading and retries a failed user catalog without losing the selected filter', async () => {
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValueOnce({ data: { success: false, message: 'unavailable' } })
    .mockResolvedValue({ data: { success: true, data: ['recovered-model'] } })
  const { change } = setup({
    value: 'history-model',
    models: ['history-model'],
  })
  await screen.findByText('Failed to load models')
  const user = userEvent.setup()
  const input = screen.getByRole('combobox', { name: 'Model Name' })
  expect(input).toHaveValue('history-model')
  await user.click(input)
  expect(screen.getByRole('option', { name: 'history-model' })).toBeVisible()
  await user.keyboard('{Escape}')
  await user.click(screen.getByRole('button', { name: 'Retry model catalog' }))
  await waitFor(() =>
    expect(screen.queryByText('Failed to load models')).not.toBeInTheDocument()
  )
  await user.click(input)
  expect(
    await screen.findByRole('option', { name: 'recovered-model' })
  ).toBeVisible()
  expect(change).not.toHaveBeenCalled()
  expect(get).toHaveBeenCalledTimes(2)
})
it.each([{ wrong: 'shape' }, ['valid-model', 42]])(
  'rejects malformed catalog %j while retaining the historical selection',
  async (data) => {
    vi.spyOn(api, 'get').mockResolvedValue({ data: { success: true, data } })
    setup({ value: 'legacy-model' })
    await screen.findByText('Failed to load models')
    expect(screen.getByRole('combobox', { name: 'Model Name' })).toHaveValue(
      'legacy-model'
    )
  }
)
it('keeps All models available for an empty catalog and shows no matching models for a nonmatching search', async () => {
  vi.spyOn(api, 'get').mockResolvedValue({ data: { success: true, data: [] } })
  setup()
  const user = userEvent.setup()
  await user.click(screen.getByRole('combobox', { name: 'Model Name' }))
  expect(screen.getAllByRole('option')).toHaveLength(1)
  await user.type(screen.getByRole('combobox'), 'missing')
  expect(screen.getByText('No matching models')).toBeVisible()
})
it('does not request or open a disabled model filter', async () => {
  const get = vi.spyOn(api, 'get')
  setup({ disabled: true })
  expect(screen.getByRole('combobox', { name: 'Model Name' })).toBeDisabled()
  expect(get).not.toHaveBeenCalled()
})
it('replaces options when user or group changes and ignores a late catalog from the previous user', async () => {
  let resolveOld: (data: unknown) => void = () => {}
  vi.spyOn(api, 'get')
    .mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolveOld = resolve
        })
    )
    .mockResolvedValueOnce({
      data: { success: true, data: ['second-account-model'] },
    })
    .mockResolvedValueOnce({
      data: { success: true, data: ['new-group-model'] },
    })
  setup()
  await act(async () => {
    useAuthStore
      .getState()
      .auth.setUser({ id: 8, username: 'second', role: 1, group: 'default' })
  })
  const user = userEvent.setup()
  await user.click(screen.getByRole('combobox', { name: 'Model Name' }))
  expect(
    await screen.findByRole('option', { name: 'second-account-model' })
  ).toBeVisible()
  await act(async () => {
    resolveOld({ data: { success: true, data: ['private-old-account-model'] } })
  })
  expect(
    screen.queryByRole('option', { name: 'private-old-account-model' })
  ).not.toBeInTheDocument()
  await act(async () => {
    useAuthStore
      .getState()
      .auth.setUser({ id: 8, username: 'second', role: 1, group: 'vip' })
  })
  expect(
    await screen.findByRole('option', { name: 'new-group-model' })
  ).toBeVisible()
  expect(
    screen.queryByRole('option', { name: 'second-account-model' })
  ).not.toBeInTheDocument()
})
