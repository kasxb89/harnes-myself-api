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
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState, type ComponentProps } from 'react'
import { beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { SettingsPageProvider } from '../../components/settings-page-context'
import { DesktopUpdateSection } from '../desktop-update-section'

type SavedValues = ComponentProps<typeof DesktopUpdateSection>['defaultValues']

function Fixture(props: { defaultValues?: SavedValues }) {
  const [container, setContainer] = useState<HTMLDivElement | null>(null)
  return (
    <>
      <div ref={setContainer} />
      <SettingsPageProvider actionsContainer={container}>
        <DesktopUpdateSection
          defaultValues={
            props.defaultValues ?? {
              'desktop_update.enabled': false,
              'desktop_update.mac_arm64_url': '',
              'desktop_update.mac_x64_url': '',
              'desktop_update.win_x64_url': '',
            }
          }
        />
      </SettingsPageProvider>
    </>
  )
}

function renderSettings(defaultValues?: SavedValues): void {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <Fixture defaultValues={defaultValues} />
    </QueryClientProvider>
  )
}

beforeEach(() => {
  vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
})

test('enabling updates saves the selected platform feed before the switch and leaves other platforms empty', async () => {
  const user = userEvent.setup()
  renderSettings()
  const feed = screen.getByRole('textbox', {
    name: 'macOS Apple Silicon update feed',
  })
  expect(feed).toHaveValue('')
  expect(
    screen.getByRole('switch', { name: 'Enable client updates' })
  ).not.toBeChecked()

  fireEvent.change(feed, {
    target: { value: 'https://updates.example.com/mac-arm64/' },
  })
  await user.click(
    screen.getByRole('switch', { name: 'Enable client updates' })
  )
  await user.click(
    screen.getByRole('button', { name: 'Save client update settings' })
  )

  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(2))
  expect(api.put).toHaveBeenNthCalledWith(1, '/api/option/', {
    key: 'desktop_update.mac_arm64_url',
    value: 'https://updates.example.com/mac-arm64/',
  })
  expect(api.put).toHaveBeenNthCalledWith(2, '/api/option/', {
    key: 'desktop_update.enabled',
    value: true,
  })
})

test('enabling without any platform feed shows a field error and sends nothing', async () => {
  const user = userEvent.setup()
  renderSettings()
  await user.click(
    screen.getByRole('switch', { name: 'Enable client updates' })
  )
  await user.click(
    screen.getByRole('button', { name: 'Save client update settings' })
  )

  expect(
    await screen.findByText(
      'Add at least one platform update feed before enabling client updates.'
    )
  ).toBeVisible()
  expect(
    screen.getByRole('textbox', { name: 'macOS Apple Silicon update feed' })
  ).toHaveAttribute('aria-invalid', 'true')
  expect(api.put).not.toHaveBeenCalled()
})

test.each([
  'https://user:password@updates.example.com/',
  'https://updates.example.com/installer.dmg',
  'http://updates.example.com/feed/',
  'https://updates.example.com/nested%2Ffeed/',
  'https://updates.example.com/manifest.json/',
  'https://updates.example.com/installer.dmg/',
  'http://127.0.0.2/feed/',
  'https:updates.example.com/feed/',
])(
  'an invalid feed %s shows a field error and sends nothing',
  async (value) => {
    const user = userEvent.setup()
    renderSettings()
    const feed = screen.getByRole('textbox', {
      name: 'Windows x64 update feed',
    })
    fireEvent.change(feed, { target: { value } })
    await user.click(
      screen.getByRole('button', { name: 'Save client update settings' })
    )

    expect(
      await screen.findByText('Invalid desktop update feed URL')
    ).toBeVisible()
    expect(feed).toHaveAttribute('aria-invalid', 'true')
    expect(api.put).not.toHaveBeenCalled()
  }
)

test('a save failure preserves entered feeds and makes saving available again', async () => {
  const user = userEvent.setup()
  vi.mocked(api.put).mockResolvedValue({
    data: { success: false, message: 'Save failed' },
  })
  renderSettings()
  const feed = screen.getByRole('textbox', { name: 'macOS Intel update feed' })
  fireEvent.change(feed, {
    target: { value: 'https://updates.example.com/mac-x64/' },
  })
  await user.click(
    screen.getByRole('button', { name: 'Save client update settings' })
  )

  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  expect(feed).toHaveValue('https://updates.example.com/mac-x64/')
  expect(
    await screen.findByRole('button', { name: 'Save client update settings' })
  ).toBeEnabled()
})

test('disabling and clearing a previously saved feed disables updates before clearing the location', async () => {
  const user = userEvent.setup()
  renderSettings({
    'desktop_update.enabled': true,
    'desktop_update.mac_arm64_url': 'https://updates.example.com/mac-arm64/',
    'desktop_update.mac_x64_url': '',
    'desktop_update.win_x64_url': '',
  })
  await user.click(
    screen.getByRole('switch', { name: 'Enable client updates' })
  )
  fireEvent.change(
    screen.getByRole('textbox', { name: 'macOS Apple Silicon update feed' }),
    { target: { value: '' } }
  )
  await user.click(
    screen.getByRole('button', { name: 'Save client update settings' })
  )

  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(2))
  expect(api.put).toHaveBeenNthCalledWith(1, '/api/option/', {
    key: 'desktop_update.enabled',
    value: false,
  })
  expect(api.put).toHaveBeenNthCalledWith(2, '/api/option/', {
    key: 'desktop_update.mac_arm64_url',
    value: '',
  })
})
