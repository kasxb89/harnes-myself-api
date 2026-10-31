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

import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { afterEach, beforeEach, expect, it } from 'vitest'

import { PricebookTable } from '../pricebook-table'
import { ycapiCatalogSchema } from '../types'

const catalog = ycapiCatalogSchema.parse(
  JSON.parse(
    readFileSync(
      path.resolve(process.cwd(), '../pkg/ycapi/catalog.json'),
      'utf8'
    )
  )
)

beforeEach(async () => {
  await i18next.changeLanguage('en')
})
afterEach(cleanup)

it('shows separate procurement, retail and official prices with billing units', () => {
  render(<PricebookTable models={catalog.models} />)
  expect(
    screen.getByRole('columnheader', { name: 'Procurement price' })
  ).toBeVisible()
  expect(
    screen.getByRole('columnheader', { name: 'Retail price' })
  ).toBeVisible()
  expect(
    screen.getByRole('columnheader', { name: 'Official benchmark' })
  ).toBeVisible()
  expect(screen.getAllByText('CNY / 1M tokens').length).toBeGreaterThan(0)
  expect(screen.getAllByText(/33\.3055$/).length).toBeGreaterThan(0)
})

it('video filtering keeps per-second prices distinct from the native clip total', async () => {
  render(<PricebookTable models={catalog.models} />)
  const user = userEvent.setup()
  await user.selectOptions(
    screen.getByRole('combobox', { name: 'Price type' }),
    'video'
  )
  await user.type(
    screen.getByRole('textbox', { name: 'Search prices' }),
    'Grok Imagine 1.5'
  )
  expect(screen.getAllByText('CNY / second').length).toBeGreaterThan(0)
  expect(screen.getByText(/0\.2638$/)).toBeVisible()
  expect(screen.getByText(/2\.638$/)).toBeVisible()
  expect(screen.queryByText('Claude Fable 5')).not.toBeInTheDocument()
})

it('an excluded model search produces an empty result instead of offering its old price', async () => {
  render(<PricebookTable models={catalog.models} />)
  await userEvent
    .setup()
    .type(
      screen.getByRole('textbox', { name: 'Search prices' }),
      'seedance-2-5-special'
    )
  expect(screen.getByText('No matching prices')).toBeVisible()
})

it('an empty catalog preserves the accessible search controls and empty state', () => {
  render(<PricebookTable models={[]} />)
  expect(screen.getByRole('textbox', { name: 'Search prices' })).toBeEnabled()
  expect(screen.getByRole('combobox', { name: 'Price type' })).toBeEnabled()
  expect(screen.getByText('No matching prices')).toBeVisible()
})
