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
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import zh from '@/i18n/locales/zh.json'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { CostRulesDialog } from '../components/cost-rules-dialog'
import type { CostRules } from '../types'

const initial: CostRules = {
  schema_version: 1,
  revision: 7,
  rules: [
    {
      channel_id: 19,
      model_name: 'previous-model',
      effective_from: 1792512000,
      kind: 'second',
      unit_cost_cny: 0.2,
      source: 'Previous verified supplier sheet',
      failure_refunded: true,
    },
  ],
}
const clients: QueryClient[] = []
const i18n = createInstance()
beforeEach(async () => {
  await i18n.init({
    lng: 'en',
    fallbackLng: 'en',
    keySeparator: false,
    nsSeparator: false,
    resources: { en, zhCN: zh },
  })
})
afterEach(() => {
  cleanup()
  clients.splice(0).forEach((client) => client.clear())
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
})

function setup() {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'root', role: ROLE.SUPER_ADMIN })
  const get = vi
    .spyOn(api, 'get')
    .mockResolvedValue({ data: { success: true, data: initial } })
  const put = vi.spyOn(api, 'put').mockImplementation(async (_url, body) => ({
    data: { success: true, data: { ...(body as CostRules), revision: 8 } },
  }))
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  clients.push(client)
  render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={client}>
        <CostRulesDialog />
      </QueryClientProvider>
    </I18nextProvider>
  )
  return { get, put }
}

async function fillVersion() {
  fireEvent.change(screen.getByLabelText('Channel ID'), {
    target: { value: '19' },
  })
  fireEvent.change(screen.getByLabelText('Exact model name'), {
    target: { value: 'new-model' },
  })
  fireEvent.change(screen.getByLabelText('Effective from (Shanghai time)'), {
    target: { value: '2026-10-22T12:00' },
  })
  fireEvent.change(screen.getByLabelText('Upstream unit cost (CNY)'), {
    target: { value: '0.1' },
  })
  fireEvent.change(screen.getByLabelText('Supplier price source'), {
    target: { value: 'Supplier published price list' },
  })
}

it('loads cost versions only when root opens the dedicated configuration dialog', async () => {
  const { get } = setup()
  expect(get).not.toHaveBeenCalled()
  fireEvent.click(
    screen.getByRole('button', { name: 'Configure upstream costs' })
  )
  const dialog = await screen.findByRole('dialog', {
    name: 'Upstream cost rules',
  })
  expect(await within(dialog).findByText('#19 · previous-model')).toBeVisible()
  expect(get).toHaveBeenCalledWith('/api/model-profit/cost-rules')
})

it('appends a cost version using the fetched revision and retains every previous version', async () => {
  const { put } = setup()
  fireEvent.click(
    screen.getByRole('button', { name: 'Configure upstream costs' })
  )
  await screen.findByLabelText('Channel ID')
  await fillVersion()
  fireEvent.click(screen.getByRole('button', { name: 'Add cost version' }))
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith('/api/model-profit/cost-rules', {
      schema_version: 1,
      revision: 7,
      rules: [
        initial.rules[0],
        {
          channel_id: 19,
          model_name: 'new-model',
          effective_from: Date.parse('2026-10-22T12:00:00+08:00') / 1000,
          kind: 'request',
          source: 'Supplier published price list',
          failure_refunded: false,
          unit_cost_cny: 0.1,
        },
      ],
    })
  )
  expect(await screen.findByText('#19 · new-model')).toBeVisible()
})

it('saves three image resolution costs and displays them without replacing history', async () => {
  const { put } = setup()
  fireEvent.click(
    screen.getByRole('button', { name: 'Configure upstream costs' })
  )
  await screen.findByLabelText('Channel ID')
  await fillVersion()
  fireEvent.click(screen.getByRole('button', { name: 'Open' }))
  fireEvent.click(
    await screen.findByRole('option', { name: 'Per image resolution' })
  )
  expect(
    screen.queryByLabelText('Upstream unit cost (CNY)')
  ).not.toBeInTheDocument()
  for (const [tier, value] of [
    ['1K', '0.1'],
    ['2K', '0.15'],
    ['4K', '0.2'],
  ]) {
    fireEvent.change(
      screen.getByLabelText(`${tier} image cost (CNY per image)`),
      { target: { value } }
    )
  }
  fireEvent.click(screen.getByRole('button', { name: 'Add cost version' }))
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith('/api/model-profit/cost-rules', {
      schema_version: 1,
      revision: 7,
      rules: [
        initial.rules[0],
        {
          channel_id: 19,
          model_name: 'new-model',
          effective_from: Date.parse('2026-10-22T12:00:00+08:00') / 1000,
          kind: 'image_resolution',
          resolution_costs_cny: { '1k': 0.1, '2k': 0.15, '4k': 0.2 },
          source: 'Supplier published price list',
          failure_refunded: false,
        },
      ],
    })
  )
  expect(
    await screen.findByText(/1K:.*0\.1.*2K:.*0\.15.*4K:.*0\.2/)
  ).toBeVisible()
  expect(screen.getByText('#19 · previous-model')).toBeVisible()
})

it('shows translated image resolution costs after switching to Chinese', async () => {
  setup()
  fireEvent.click(
    screen.getByRole('button', { name: 'Configure upstream costs' })
  )
  await screen.findByLabelText('Channel ID')
  await act(async () => {
    await i18n.changeLanguage('zhCN')
  })
  fireEvent.click(screen.getByRole('button', { name: zh.translation.Open }))
  fireEvent.click(await screen.findByRole('option', { name: '按图片分辨率' }))
  expect(screen.getByLabelText('1K 图片成本（元/张）')).toBeVisible()
  expect(screen.getByLabelText('2K 图片成本（元/张）')).toBeVisible()
  expect(screen.getByLabelText('4K 图片成本（元/张）')).toBeVisible()
})

it('keeps invalid cost input visible without submitting it', async () => {
  const { put } = setup()
  fireEvent.click(
    screen.getByRole('button', { name: 'Configure upstream costs' })
  )
  await screen.findByLabelText('Channel ID')
  fireEvent.click(screen.getByRole('button', { name: 'Add cost version' }))
  expect(await screen.findByText('Enter a positive channel ID')).toBeVisible()
  expect(screen.getByLabelText('Channel ID')).toHaveAttribute(
    'aria-invalid',
    'true'
  )
  expect(put).not.toHaveBeenCalled()
})

it('preserves unsaved input and refreshes the revision when saving is rejected', async () => {
  const { put, get } = setup()
  put.mockResolvedValue({
    data: { success: false, message: 'Cost rules revision changed' },
  })
  fireEvent.click(
    screen.getByRole('button', { name: 'Configure upstream costs' })
  )
  await screen.findByLabelText('Channel ID')
  await fillVersion()
  fireEvent.click(screen.getByRole('button', { name: 'Add cost version' }))
  await waitFor(() => expect(get).toHaveBeenCalledTimes(2))
  expect(screen.getByLabelText('Exact model name')).toHaveValue('new-model')
  expect(screen.getByLabelText('Upstream unit cost (CNY)')).toHaveValue(0.1)
})
