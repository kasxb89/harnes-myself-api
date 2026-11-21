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
import { act, cleanup, render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, expect, test, vi } from 'vitest'

import { ObsidianWorkspace } from '../components/obsidian-workspace'

afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})

function motionPreference(reduced: boolean) {
  vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
    matches: reduced && query.includes('prefers-reduced-motion'),
    media: query,
    onchange: null,
    addListener: () => undefined,
    removeListener: () => undefined,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
    dispatchEvent: () => false,
  }))
}

test('pausing and resuming workspace effects keeps an edited draft mounted and unchanged', async () => {
  motionPreference(false)
  const user = userEvent.setup()
  render(
    <ObsidianWorkspace>
      <label>
        Reply draft
        <input />
      </label>
    </ObsidianWorkspace>
  )
  const draft = screen.getByRole('textbox', { name: 'Reply draft' })
  await user.type(draft, 'Keep this original draft')
  const pause = screen.getByRole('button', { name: 'Pause effects' })
  expect(pause).toHaveAttribute('aria-pressed', 'true')
  await user.click(pause)
  expect(
    screen.getByRole('button', { name: 'Enable effects' })
  ).toHaveAttribute('aria-pressed', 'false')
  expect(draft).toHaveValue('Keep this original draft')
  await user.click(screen.getByRole('button', { name: 'Enable effects' }))
  expect(screen.getByRole('button', { name: 'Pause effects' })).toHaveAttribute(
    'aria-pressed',
    'true'
  )
  expect(screen.getByRole('textbox', { name: 'Reply draft' })).toBe(draft)
  expect(draft).toHaveValue('Keep this original draft')
})

test('system reduced motion keeps the workspace static and disables the animation toggle', () => {
  motionPreference(true)
  const { container } = render(
    <ObsidianWorkspace>Workspace content</ObsidianWorkspace>
  )
  expect(
    screen.getByRole('button', { name: 'System reduced motion is enabled' })
  ).toBeDisabled()
  expect(container.querySelector('.obsidian-workspace')).toHaveAttribute(
    'data-effects-running',
    'false'
  )
  expect(container.querySelector('.relay-obsidian-object')).toHaveAttribute(
    'data-paused',
    'true'
  )
})

test('a hidden document pauses decorations and becoming visible respects the manual pause', async () => {
  motionPreference(false)
  const visibility = vi.spyOn(document, 'visibilityState', 'get')
  visibility.mockReturnValue('visible')
  const { container } = render(
    <ObsidianWorkspace>Workspace content</ObsidianWorkspace>
  )
  const workspace = container.querySelector('.obsidian-workspace')
  expect(workspace).toHaveAttribute('data-effects-running', 'true')
  act(() => {
    visibility.mockReturnValue('hidden')
    document.dispatchEvent(new Event('visibilitychange'))
  })
  expect(workspace).toHaveAttribute('data-effects-running', 'false')
  act(() => {
    visibility.mockReturnValue('visible')
    document.dispatchEvent(new Event('visibilitychange'))
  })
  await userEvent.click(screen.getByRole('button', { name: 'Pause effects' }))
  act(() => {
    visibility.mockReturnValue('hidden')
    document.dispatchEvent(new Event('visibilitychange'))
    visibility.mockReturnValue('visible')
    document.dispatchEvent(new Event('visibilitychange'))
  })
  expect(workspace).toHaveAttribute('data-effects-running', 'false')
  expect(
    screen.getByRole('button', { name: 'Enable effects' })
  ).toHaveAttribute('aria-pressed', 'false')
})

test('a dedicated operations stage removes the shared canvas while keeping working content mounted', async () => {
  motionPreference(false)
  const content = (
    <label>
      Reply draft
      <input />
    </label>
  )
  const view = render(<ObsidianWorkspace>{content}</ObsidianWorkspace>)
  const draft = screen.getByRole('textbox', { name: 'Reply draft' })
  await userEvent.type(draft, 'Keep working content')
  view.rerender(
    <ObsidianWorkspace showStage={false}>{content}</ObsidianWorkspace>
  )
  expect(
    screen.queryByRole('button', { name: 'Pause effects' })
  ).not.toBeInTheDocument()
  expect(view.container.querySelector('canvas')).not.toBeInTheDocument()
  expect(screen.getByRole('textbox', { name: 'Reply draft' })).toBe(draft)
  expect(draft).toHaveValue('Keep working content')
})
