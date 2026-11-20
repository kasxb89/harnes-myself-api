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
import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { RelayAccessGuide } from '../guide'
import {
  buildRelayAccessGuide,
  getRelayAccessGuideSections,
  normalizeRelayRoot,
  RELAY_ACCESS_GUIDE_FILENAME,
} from '../guide-content'

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('relay guide address handling', () => {
  it.each([
    ['https://gateway.example/harness/', 'https://gateway.example/harness'],
    ['https://gateway.example/harness/v1/', 'https://gateway.example/harness'],
    ['http://127.0.0.1:43210/local', 'http://127.0.0.1:43210/local'],
    ['http://127.0.0.2:43210/local', 'http://127.0.0.2:43210/local'],
    ['http://[::1]:43210/local', 'http://[::1]:43210/local'],
    [
      'https://gateway.example/control%20panel',
      'https://gateway.example/control%20panel',
    ],
  ])(
    'preserves an allowed root and strips only its trailing API version: %s',
    (value, expected) => {
      expect(normalizeRelayRoot(value)).toBe(expected)
    }
  )

  it.each([
    'http://gateway.example/harness',
    'http://127.evil.example/harness',
    'https://user:secret@gateway.example/harness',
    'https://gateway.example/harness?',
    'https://gateway.example/harness#',
    'https://gateway.example/harness?token=secret',
    'https://gateway.example/harness/v1/v1',
    'https://gateway.example/v1/chat/completions',
    'https://gateway.example/path%0asecret',
    'javascript:alert(1)',
    undefined,
  ])('keeps an unsafe or ambiguous root out of the manual: %s', (value) => {
    expect(normalizeRelayRoot(value)).toBeUndefined()
  })

  it('uses a clearly unresolved environment placeholder instead of inventing a host', () => {
    const guide = buildRelayAccessGuide('https://user:secret@gateway.example')
    expect(guide).toContain('$YUNSHU_ROOT/v1/models')
    expect(guide).toContain('待填写的地址变量')
    expect(guide).not.toContain('secret')
    expect(guide).not.toContain('gateway.example')
  })

  it('uses the same structured sections for the manual and preserves exact endpoint prefixes', () => {
    const root = 'https://gateway.example/harness/v1'
    const guide = buildRelayAccessGuide(root)
    for (const section of getRelayAccessGuideSections(root)) {
      expect(guide).toContain(section.markdown)
    }
    expect(guide).toMatch(/^# 云枢 AI 中转接入说明书\n\n/)
    expect(guide).toContain(
      'https://gateway.example/harness/v1/chat/completions'
    )
    expect(guide).toContain('https://gateway.example/harness/v1/responses')
    expect(guide).not.toContain('/v1/v1')
    expect(guide).toContain('JWT')
    expect(guide).toContain('PAT')
    expect(guide).toContain('data[].id')
    expect(guide).toContain('继续查询原任务，不要重新创建')
    expect(guide).not.toMatch(/YCAPI|CodeBloom|DMXAPI|AutoDL|8\.133\.202\.230/)
    expect(guide).not.toMatch(/本次开发|源码核实|本稿不提供/)
  })
})

describe('relay guide reader', () => {
  it('keeps all Chinese manual sections visible with navigable section anchors', () => {
    render(<RelayAccessGuide rootURL='https://gateway.example/harness' />)
    const article = screen.getByRole('article', { name: 'Relay access guide' })
    expect(article).toHaveAttribute('lang', 'zh-CN')
    expect(within(article).getAllByRole('heading', { level: 2 })).toHaveLength(
      5
    )
    for (const heading of within(article).getAllByRole('heading', {
      level: 2,
    })) {
      expect(heading).toBeVisible()
    }
    const navigation = screen.getByRole('navigation', {
      name: 'Guide contents',
    })
    const links = within(navigation).getAllByRole('link')
    expect(links).toHaveLength(5)
    for (const link of links) {
      const id = link.getAttribute('href')?.slice(1)
      expect(id).toBeTruthy()
      expect(document.querySelector(`#${id}`)).toBeVisible()
    }
    expect(
      within(article).getByText('https://gateway.example/harness/v1/messages')
    ).toBeVisible()
    expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
  })

  it('copies the full current manual using the shared copy interaction', async () => {
    const user = userEvent.setup()
    const write = vi.spyOn(navigator.clipboard, 'writeText')
    const root = 'https://gateway.example/harness'
    const view = render(<RelayAccessGuide rootURL={root} />)
    view.rerender(<RelayAccessGuide rootURL={`${root}/new`} />)
    await user.click(screen.getByRole('button', { name: 'Copy guide' }))
    await waitFor(() =>
      expect(write).toHaveBeenCalledWith(buildRelayAccessGuide(`${root}/new`))
    )
    expect(screen.getByRole('button', { name: 'Copied' })).toBeVisible()
  })

  it('downloads the exact shared Markdown with its Chinese filename without submitting requests', async () => {
    const user = userEvent.setup()
    const createObjectURL = vi
      .fn<(blob: Blob) => string>()
      .mockReturnValue('blob:manual-download')
    const revokeObjectURL = vi.fn()
    const BaseURL = URL
    vi.stubGlobal(
      'URL',
      class extends BaseURL {
        static createObjectURL = createObjectURL
        static revokeObjectURL = revokeObjectURL
      }
    )
    let downloadName = ''
    let downloadHref = ''
    const click = vi
      .spyOn(HTMLAnchorElement.prototype, 'click')
      .mockImplementation(function (this: HTMLAnchorElement) {
        downloadName = this.download
        downloadHref = this.href
      })
    const fetch = vi.spyOn(globalThis, 'fetch')
    render(<RelayAccessGuide rootURL='https://gateway.example/harness' />)
    await user.click(screen.getByRole('button', { name: 'Download Markdown' }))
    expect(createObjectURL).toHaveBeenCalledOnce()
    const blob = createObjectURL.mock.calls[0][0]
    expect(blob.type).toBe('text/markdown;charset=utf-8')
    const reader = new FileReader()
    const text = new Promise<string>((resolve, reject) => {
      reader.addEventListener('load', () => resolve(String(reader.result)), {
        once: true,
      })
      reader.addEventListener('error', () => reject(reader.error), {
        once: true,
      })
    })
    reader.readAsText(blob)
    expect(await text).toBe(
      buildRelayAccessGuide('https://gateway.example/harness')
    )
    expect(click).toHaveBeenCalledOnce()
    expect(downloadName).toBe(RELAY_ACCESS_GUIDE_FILENAME)
    expect(downloadHref).toBe('blob:manual-download')
    expect(revokeObjectURL).toHaveBeenCalledWith('blob:manual-download')
    expect(fetch).not.toHaveBeenCalled()
  })
})
