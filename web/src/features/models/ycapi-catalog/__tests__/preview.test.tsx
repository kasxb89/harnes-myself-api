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
import { readFileSync } from 'node:fs'
import path from 'node:path'

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, cleanup, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { beforeEach, afterEach, expect, it, vi } from 'vitest'

import { toIntlLocale } from '@/i18n/languages'
import { api } from '@/lib/api'

import { formatCatalogCny } from '../price-format'
import { QuotePreviewForm } from '../quote-preview'
import { ycapiCatalogSchema } from '../types'

const catalog = ycapiCatalogSchema.parse(
  JSON.parse(
    readFileSync(
      path.resolve(process.cwd(), '../pkg/ycapi/catalog.json'),
      'utf8'
    )
  )
)
const clients: QueryClient[] = []
beforeEach(async () => {
  await i18next.changeLanguage('en')
})
afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  clients.forEach((c) => c.clear())
  clients.length = 0
})
function renderPreview(id: string) {
  const model = catalog.models.find((m) => m.id === id)
  if (!model) throw new Error('Missing approved model fixture')
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <QuotePreviewForm model={model} />
    </QueryClientProvider>
  )
  return model
}
it('preserves the approved61 models and149 price rows in the API schema', () => {
  expect(catalog.models).toHaveLength(61)
  expect(
    catalog.models.reduce((n, m) => n + (m.priceRows?.length || 0), 0)
  ).toBe(149)
  expect(catalog.generationEnabled).toBe(false)
})
it('video selection submits the whole selected specification and clears an obsolete quote', async () => {
  const post = vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: true,
      data: {
        retailCny: '0.864',
        procurementCny: '0.364',
        officialCny: '2.66444',
        endpoint: '/v1/videos/generations',
        upstreamBody: { duration: '5' },
        generationEnabled: false,
      },
    },
  })
  const model = renderPreview('ycapi-grok-imagine-video')
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Calculate preview' }))
  await screen.findByText(/No funds reserved\./)
  expect(post.mock.calls[0][0]).toBe('/api/ycapi/quote-preview')
  expect(post.mock.calls[0][1]).toMatchObject({
    modelId: model.id,
    specId: model.retailOffers[0].id,
  })
  await user.selectOptions(
    screen.getByLabelText('Specification'),
    model.retailOffers[1].id
  )
  expect(screen.queryByText(/No funds reserved\./)).not.toBeInTheDocument()
})
it('edited prompt prevents an older in-flight quote from being shown', async () => {
  let finish!: (v: unknown) => void
  vi.spyOn(api, 'post').mockImplementation(
    () =>
      new Promise((resolve) => {
        finish = resolve
      })
  )
  renderPreview('ycapi-gpt-6.1-sol')
  const user = userEvent.setup()
  await user.click(screen.getByRole('button', { name: 'Calculate preview' }))
  await user.type(screen.getByLabelText('Prompt'), ' changed')
  finish({
    data: {
      success: true,
      data: {
        retailCny: '1',
        procurementCny: '0.2',
        officialCny: '2',
        upstreamBody: {},
        generationEnabled: false,
      },
    },
  })
  await waitFor(() =>
    expect(
      screen.getByRole('button', { name: 'Calculate preview' })
    ).toBeEnabled()
  )
  expect(screen.queryByText(/No funds reserved\./)).not.toBeInTheDocument()
})
it('invalid negative token quantities are refused without an API request', async () => {
  const post = vi.spyOn(api, 'post')
  renderPreview('ycapi-gpt-6.1-sol')
  const user = userEvent.setup()
  await user.clear(screen.getByLabelText('Example input tokens'))
  await user.type(screen.getByLabelText('Example input tokens'), '-1')
  await user.click(screen.getByRole('button', { name: 'Calculate preview' }))
  expect(post).not.toHaveBeenCalled()
})
it('fixed CNY prices format safely for all interface languages', () => {
  for (const language of [
    'zhCN',
    'zhTW',
    'en',
    'fr',
    'ru',
    'ja',
    'vi',
    'invalid-language',
  ]) {
    expect(formatCatalogCny(0.123456, toIntlLocale(language))).toBeTruthy()
  }
})
