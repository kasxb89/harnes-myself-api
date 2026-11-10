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
import { fireEvent, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { expect, test } from 'vitest'

import { OfficialReferencesForm } from '../components/official-references-form'
import {
  newOfficialReference,
  officialReferenceValid,
} from '../lib/official-references'
import type {
  OfficialReferenceConfiguration,
  OfficialReferenceRow,
} from '../unified-api'

const row = {
  params: { size: '1:1', resolution: '2K', mode: 'text_to_image' },
  unit: 'CNY / image',
  official_reference: {
    schema_version: 1,
    currency: 'CNY',
    official_cny: '0.123456789012345678901234',
    model: 'Official model',
    unit: 'CNY / image',
    source_url: 'https://example.com/pricing',
    checked_at: '2026-11-09',
    conditions: 'Standard generation',
    source_currency: 'USD',
    usd_cny: '7.3',
  },
} satisfies OfficialReferenceRow

function Editor({ initial }: { initial?: OfficialReferenceConfiguration }) {
  const [value, setValue] = useState(initial)
  return (
    <>
      <OfficialReferencesForm
        value={value}
        specifications={[row]}
        modelName='fixture-image'
        onChange={setValue}
      />
      <output data-testid='draft'>{JSON.stringify(value)}</output>
    </>
  )
}

test('official references preserve full decimal precision and reject misleading or incomplete values', () => {
  expect(officialReferenceValid(row)).toBe(true)
  for (const changes of [
    { official_cny: '00.5' },
    { official_cny: '0' },
    { official_cny: '1e-4' },
    { model: ' model ' },
    { conditions: ' conditions ' },
    { source_url: 'http://example.com' },
    { source_url: 'https://user:secret@example.com' },
    { checked_at: '2026-02-30' },
    { checked_at: '9999-01-01' },
    { usd_cny: '' },
  ]) {
    expect(
      officialReferenceValid({
        ...row,
        official_reference: { ...row.official_reference, ...changes },
      })
    ).toBe(false)
  }
  expect(newOfficialReference(row, 'fallback')).toEqual(row)
})

test('inherited references are read-only and edits retain the exact image specification and FX evidence', async () => {
  render(<Editor />)
  expect(screen.getByLabelText('Official price in CNY')).toBeDisabled()
  expect(screen.getByLabelText('Official price in CNY')).toHaveValue(
    '0.123456789012345678901234'
  )
  await userEvent.click(screen.getByLabelText('Reference source'))
  await userEvent.click(
    screen.getByRole('option', { name: 'Configure official references' })
  )
  fireEvent.change(screen.getByLabelText('Official price in CNY'), {
    target: { value: '0.333333333333333333333333' },
  })
  const draft = JSON.parse(
    screen.getByTestId('draft').textContent ?? ''
  ) as OfficialReferenceConfiguration
  expect(draft.rows?.[0]?.params).toEqual(row.params)
  expect(draft.rows?.[0]?.official_reference?.official_cny).toBe(
    '0.333333333333333333333333'
  )
  expect(draft.rows?.[0]?.official_reference?.usd_cny).toBe('7.3')
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
})

test('invalid reference blocks a draft with inline feedback; withdrawal is explicit and contains no rows', async () => {
  const { container } = render(
    <Editor initial={{ mode: 'override', rows: [row] }} />
  )
  fireEvent.change(screen.getByLabelText('Official price in CNY'), {
    target: { value: '0' },
  })
  expect(screen.getByRole('alert')).toHaveTextContent(
    'Complete the official price'
  )
  expect(
    container.querySelector('[data-official-reference-invalid="true"]')
  ).toBeInTheDocument()
  await userEvent.click(screen.getByLabelText('Reference source'))
  await userEvent.click(
    screen.getByRole('option', { name: 'Disable price comparisons' })
  )
  expect(JSON.parse(screen.getByTestId('draft').textContent ?? '')).toEqual({
    mode: 'disabled',
  })
  expect(screen.queryByRole('alert')).not.toBeInTheDocument()
  expect(
    screen.queryByLabelText('Official price in CNY')
  ).not.toBeInTheDocument()
})
