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
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, test, vi } from 'vitest'

import { DateTimePicker } from '../datetime-picker'

const value = new Date(2026, 9, 11, 8, 30, 45)

test('opens one calendar and a seconds editor and commits the edited timestamp only on confirmation', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  render(<DateTimePicker value={value} onChange={onChange} />)
  await user.click(screen.getByRole('button', { name: '2026-10-11 08:30:45' }))
  expect(screen.getByRole('group', { name: 'Time' })).toBeVisible()
  expect(screen.getByLabelText('Time · Seconds')).toHaveValue('45')
  fireEvent.change(screen.getByLabelText('Time · Hours'), {
    target: { value: '09' },
  })
  fireEvent.change(screen.getByLabelText('Time · Minutes'), {
    target: { value: '15' },
  })
  expect(onChange).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Confirm' }))
  expect(onChange).toHaveBeenCalledExactlyOnceWith(
    new Date(2026, 9, 11, 9, 15, 45)
  )
})

test('cancel and escape discard edits and reopening restores the committed value', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  render(<DateTimePicker value={value} onChange={onChange} />)
  const trigger = screen.getByRole('button', { name: /2026-10-11/ })
  await user.click(trigger)
  fireEvent.change(screen.getByLabelText('Time · Hours'), {
    target: { value: '20' },
  })
  await user.click(screen.getByRole('button', { name: 'Cancel' }))
  await user.click(trigger)
  expect(screen.getByLabelText('Time · Hours')).toHaveValue('08')
  await user.keyboard('{Escape}')
  await waitFor(() => expect(trigger).toHaveFocus())
  expect(onChange).not.toHaveBeenCalled()
})

test('invalid or half-cleared times cannot produce an invalid Date or silently change an expiry', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  render(<DateTimePicker value={value} onChange={onChange} />)
  await user.click(screen.getByRole('button', { name: /2026-10-11/ }))
  fireEvent.change(screen.getByLabelText('Time · Hours'), {
    target: { value: '25' },
  })
  expect(screen.getByRole('button', { name: 'Confirm' })).toBeDisabled()
  expect(screen.getByRole('alert')).toBeVisible()
  fireEvent.change(screen.getByLabelText('Time · Hours'), {
    target: { value: '' },
  })
  expect(screen.getByRole('button', { name: 'Confirm' })).toBeDisabled()
  expect(onChange).not.toHaveBeenCalled()
})

test('a future expiry can be entered and seconds are preserved through confirmation', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  render(<DateTimePicker onChange={onChange} />)
  await user.click(screen.getByRole('button', { name: 'Select date' }))
  fireEvent.change(screen.getByLabelText('Date', { exact: true }), {
    target: { value: '2030-12-31' },
  })
  fireEvent.change(screen.getByLabelText('Time · Hours'), {
    target: { value: '23' },
  })
  fireEvent.change(screen.getByLabelText('Time · Minutes'), {
    target: { value: '59' },
  })
  fireEvent.change(screen.getByLabelText('Time · Seconds'), {
    target: { value: '58' },
  })
  await user.click(screen.getByRole('button', { name: 'Confirm' }))
  expect(onChange).toHaveBeenCalledExactlyOnceWith(
    new Date(2030, 11, 31, 23, 59, 58)
  )
})

test('clearing the timestamp remains explicit and emits undefined once', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  render(<DateTimePicker value={value} onChange={onChange} />)
  await user.click(screen.getByRole('button', { name: /2026-10-11/ }))
  await user.click(screen.getByRole('button', { name: 'Clear' }))
  expect(onChange).toHaveBeenCalledExactlyOnceWith(undefined)
})
