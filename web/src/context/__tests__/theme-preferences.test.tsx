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
import { cleanup, fireEvent, render, screen } from '@testing-library/react'
import { afterEach, expect, test, vi } from 'vitest'

import { ConfigDrawer } from '@/components/config-drawer'
import { ThemeQuickSwitcher } from '@/components/theme-quick-switcher'
import { ThemeSwitch } from '@/components/theme-switch'
import {
  ThemeCustomizationProvider,
  useThemeCustomization,
} from '@/context/theme-customization-provider'
import { ThemeProvider, useTheme } from '@/context/theme-provider'
import { useAuthStore } from '@/stores/auth-store'

function Controls() {
  const theme = useTheme()
  const customization = useThemeCustomization()
  return (
    <>
      <output aria-label='Theme mode'>{theme.theme}</output>
      <output aria-label='Theme preset'>
        {customization.customization.preset}
      </output>
      <button
        type='button'
        onClick={() => {
          theme.setTheme('light')
          customization.setFont('serif')
          customization.setRadius('xl')
          customization.setContentLayout('centered')
        }}
      >
        Legacy changes
      </button>
      <button
        type='button'
        onClick={() => {
          theme.resetTheme()
          customization.resetCustomization()
        }}
      >
        Reset
      </button>
      <ThemeSwitch />
      <ThemeQuickSwitcher />
      <ConfigDrawer />
    </>
  )
}
function Fixture() {
  return (
    <ThemeProvider>
      <ThemeCustomizationProvider>
        <Controls />
      </ThemeCustomizationProvider>
    </ThemeProvider>
  )
}
afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
  localStorage.clear()
  useAuthStore.getState().auth.reset()
  document.cookie = 'ui-test-auth=;path=/;max-age=0'
})

test('one Obsidian system ignores old mode and preset values without rewriting saved data', () => {
  localStorage.setItem('newapi:theme:v1:mode', 'light')
  localStorage.setItem('newapi:theme:v1:preset', 'rose-garden')
  localStorage.setItem('newapi:theme:v1:font', 'serif')
  localStorage.setItem('unrelated-session-test', 'retain-me')
  const before = { ...localStorage }
  render(<Fixture />)
  expect(screen.getByLabelText('Theme mode')).toHaveTextContent('dark')
  expect(screen.getByLabelText('Theme preset')).toHaveTextContent('obsidian')
  expect(document.documentElement).toHaveClass('dark')
  expect(document.body).toHaveAttribute('data-theme-font', 'sans')
  expect({ ...localStorage }).toEqual(before)
  expect(
    screen.queryByRole('button', { name: 'Toggle theme' })
  ).not.toBeInTheDocument()
  expect(screen.queryByRole('radiogroup')).not.toBeInTheDocument()
})

test('legacy setters and resets keep the visual world and current authentication untouched', () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 77, username: 'synthetic-ui-test', role: 1 })
  document.cookie = 'ui-test-auth=keep;path=/'
  const before = useAuthStore.getState().auth
  render(<Fixture />)
  fireEvent.click(screen.getByText('Legacy changes'))
  fireEvent.click(screen.getByText('Reset'))
  expect(screen.getByLabelText('Theme mode')).toHaveTextContent('dark')
  expect(screen.getByLabelText('Theme preset')).toHaveTextContent('obsidian')
  expect(document.body).toHaveAttribute('data-theme-font', 'sans')
  expect(useAuthStore.getState().auth).toBe(before)
  expect(document.cookie).toContain('ui-test-auth=keep')
})

test('fixed styling also works when preference storage is unavailable', () => {
  vi.spyOn(localStorage, 'getItem').mockImplementation(() => {
    throw new DOMException('unavailable', 'SecurityError')
  })
  render(<Fixture />)
  expect(screen.getByLabelText('Theme preset')).toHaveTextContent('obsidian')
  expect(document.documentElement).toHaveClass('dark')
})
