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

import { parseDateTime } from '../date-time-utils'
import { TimeInput } from '../time-input'

function Editor() {
  const [value, setValue] = useState('08:30:45')
  return (
    <>
      <TimeInput label='Time' value={value} onChange={setValue} />
      <output aria-label='Selected time'>{value}</output>
    </>
  )
}

test('typing two digits advances through hour/minute/second fields without reverting a previous field', async () => {
  const user = userEvent.setup()
  render(<Editor />)
  await user.click(screen.getByLabelText('Time · Hours'))
  await user.keyboard('23')
  expect(screen.getByLabelText('Time · Minutes')).toHaveFocus()
  await user.keyboard('59')
  expect(screen.getByLabelText('Time · Seconds')).toHaveFocus()
  await user.keyboard('58')
  expect(
    screen.getByRole('status', { name: 'Selected time' })
  ).toHaveTextContent('23:59:58')
  await user.keyboard('{ArrowUp}{ArrowUp}')
  expect(
    screen.getByRole('status', { name: 'Selected time' })
  ).toHaveTextContent('23:59:00')
})

test('pasting a full time with or without seconds preserves all fields', async () => {
  const user = userEvent.setup()
  render(<Editor />)
  await user.click(screen.getByLabelText('Time · Hours'))
  await user.paste('09:15:38')
  expect(
    screen.getByRole('status', { name: 'Selected time' })
  ).toHaveTextContent('09:15:38')
  await user.paste('20:01')
  expect(
    screen.getByRole('status', { name: 'Selected time' })
  ).toHaveTextContent('20:01:00')
})

test('a one-digit field pads on blur, while impossible pasted times leave the draft unchanged', async () => {
  const user = userEvent.setup()
  render(<Editor />)
  fireEvent.change(screen.getByLabelText('Time · Hours'), {
    target: { value: '8' },
  })
  fireEvent.blur(screen.getByLabelText('Time · Hours'))
  expect(screen.getByLabelText('Time · Hours')).toHaveValue('08')
  await user.click(screen.getByLabelText('Time · Hours'))
  await user.paste('25:90:99')
  expect(
    screen.getByRole('status', { name: 'Selected time' })
  ).toHaveTextContent('08:30:45')
})

test.each(['2026-02-30', '2026-13-01', '2026-1-01'])(
  'rejects invalid calendar date %s instead of rolling it into another day',
  (value) => expect(parseDateTime(value, '08:00:00')).toBeUndefined()
)
