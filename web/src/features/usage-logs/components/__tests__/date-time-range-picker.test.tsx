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
import i18next from 'i18next'
import { afterEach, expect, test, vi } from 'vitest'

import { CompactDateTimeRangePicker } from '../compact-date-time-range-picker'

const range = {
  start: new Date(2026, 9, 20, 8, 30, 15),
  end: new Date(2026, 9, 21, 17, 45, 59),
}

afterEach(async () => {
  vi.useRealTimers()
  await i18next.changeLanguage('en')
})

test('one calendar pane edits the range without nested date dialogs and cancel restores the original draft', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  render(<CompactDateTimeRangePicker {...range} onChange={onChange} />)
  const trigger = screen.getByRole('button', { name: /2026-10-20/ })
  await user.click(trigger)
  expect(screen.getAllByRole('grid')).toHaveLength(1)
  expect(
    screen.queryByRole('button', { name: 'Choose start date' })
  ).not.toBeInTheDocument()
  fireEvent.change(screen.getByLabelText('Start date'), {
    target: { value: '2026-10-19' },
  })
  fireEvent.change(screen.getByLabelText('Start Time · Seconds'), {
    target: { value: '33' },
  })
  expect(onChange).not.toHaveBeenCalled()
  await user.click(screen.getByRole('button', { name: 'Cancel' }))
  await user.click(trigger)
  expect(screen.getByLabelText('Start date')).toHaveValue('2026-10-20')
  expect(screen.getByLabelText('Start Time · Seconds')).toHaveValue('15')
  await user.keyboard('{Escape}')
  await waitFor(() => expect(trigger).toHaveFocus())
  expect(onChange).not.toHaveBeenCalled()
})

test('a partial range remains supported and clearing explicitly restores the caller default', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  render(<CompactDateTimeRangePicker start={range.start} onChange={onChange} />)
  await user.click(screen.getByRole('button', { name: /2026-10-20/ }))
  await user.click(screen.getByRole('button', { name: 'Confirm' }))
  expect(onChange).toHaveBeenLastCalledWith({
    start: range.start,
    end: undefined,
  })
  await user.click(screen.getByRole('button', { name: /2026-10-20/ }))
  await user.click(screen.getByRole('button', { name: 'Clear' }))
  expect(onChange).toHaveBeenLastCalledWith({
    start: undefined,
    end: undefined,
  })
})

test('rolling shortcuts apply immediately with exact instants and preserve seconds', async () => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(new Date(2026, 9, 21, 12, 34, 56))
  const user = userEvent.setup()
  const onChange = vi.fn()
  render(<CompactDateTimeRangePicker {...range} onChange={onChange} />)
  await user.click(screen.getByRole('button', { name: /2026-10-20/ }))
  await user.click(screen.getByRole('button', { name: 'Last hour' }))
  expect(onChange).toHaveBeenCalledExactlyOnceWith({
    start: new Date(2026, 9, 21, 11, 34, 56),
    end: new Date(2026, 9, 21, 12, 34, 56),
  })
})

test('Chinese weekly shortcut starts on Monday like its calendar', async () => {
  vi.useFakeTimers({ toFake: ['Date'] })
  vi.setSystemTime(new Date(2026, 9, 21, 12))
  i18next.addResourceBundle('zhCN', 'translation', {}, true, true)
  await i18next.changeLanguage('zhCN')
  const user = userEvent.setup()
  const onChange = vi.fn()
  render(<CompactDateTimeRangePicker {...range} onChange={onChange} />)
  await user.click(screen.getByRole('button', { name: /2026-10-20/ }))
  await user.click(screen.getByRole('button', { name: i18next.t('This week') }))
  expect(onChange).toHaveBeenCalledExactlyOnceWith({
    start: new Date(2026, 9, 19),
    end: new Date(2026, 9, 25, 23, 59, 59, 999),
  })
})

test('two calendar clicks replace the whole previous range and preserve both endpoint times', async () => {
  const user = userEvent.setup()
  const onChange = vi.fn()
  render(<CompactDateTimeRangePicker {...range} onChange={onChange} />)
  await user.click(screen.getByRole('button', { name: /2026-10-20/ }))
  await user.click(screen.getByRole('button', { name: /October 25th, 2026/ }))
  expect(screen.getByLabelText('Start date')).toHaveValue('2026-10-25')
  expect(screen.getByLabelText('End date')).toHaveValue('')
  expect(screen.getByRole('button', { name: 'Confirm' })).toBeDisabled()
  await user.click(screen.getByRole('button', { name: /October 27th, 2026/ }))
  await user.click(screen.getByRole('button', { name: 'Confirm' }))
  expect(onChange).toHaveBeenCalledExactlyOnceWith({
    start: new Date(2026, 9, 25, 8, 30, 15),
    end: new Date(2026, 9, 27, 17, 45, 59),
  })
})
