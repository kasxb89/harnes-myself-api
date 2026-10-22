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
import { cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { useState } from 'react'
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import {
  MediaOperationsFilters,
  type MediaFiltersDraft,
} from '../components/filters'

const i18n = createInstance()
beforeEach(async () => {
  await i18n.init({
    lng: 'en',
    fallbackLng: 'en',
    keySeparator: false,
    nsSeparator: false,
  })
})
afterEach(cleanup)

function setup(options: { loading?: boolean; invalid?: boolean } = {}) {
  const apply = vi.fn()
  const reset = vi.fn()
  function Harness() {
    const [value, setValue] = useState<MediaFiltersDraft>({
      start: new Date('2026-10-20T00:00:00Z'),
      end: new Date('2026-10-21T00:00:00Z'),
      kind: 'all',
      model: '',
    })
    return (
      <MediaOperationsFilters
        value={value}
        models={['MiniMax-H3', 'yunshu-internal-gpt-image-2']}
        onChange={setValue}
        onApply={() => apply(value)}
        onReset={reset}
        loading={options.loading ?? false}
        invalid={options.invalid ?? false}
      />
    )
  }
  render(
    <I18nextProvider i18n={i18n}>
      <Harness />
    </I18nextProvider>
  )
  return { apply, reset }
}

it('keyboard users can choose media and a model before explicitly applying filters', async () => {
  const callbacks = setup()
  const user = userEvent.setup()
  const kind = screen.getByRole('combobox', { name: 'Media type' })
  kind.focus()
  await user.keyboard('{ArrowDown}')
  await user.click(await screen.findByRole('option', { name: 'Video' }))
  const model = screen.getByRole('combobox', { name: 'Model' })
  await user.click(model)
  await user.clear(model)
  await user.type(model, 'MiniMax')
  await screen.findByRole('option', { name: 'MiniMax-H3' })
  await user.keyboard('{ArrowDown}{Enter}')
  expect(callbacks.apply).not.toHaveBeenCalled()
  const submit = screen.getByRole('button', { name: 'Apply filters' })
  submit.focus()
  await user.keyboard('{Enter}')
  expect(callbacks.apply).toHaveBeenCalledWith(
    expect.objectContaining({ kind: 'video', model: 'MiniMax-H3' })
  )
})

it.each([{ loading: true }, { invalid: true }])(
  'pending or invalid filters cannot submit a query (%j)',
  async (options) => {
    const callbacks = setup(options)
    expect(screen.getByRole('button', { name: 'Apply filters' })).toBeDisabled()
    await userEvent.click(screen.getByRole('button', { name: 'Reset' }))
    expect(callbacks.reset).toHaveBeenCalledOnce()
    expect(callbacks.apply).not.toHaveBeenCalled()
  }
)
