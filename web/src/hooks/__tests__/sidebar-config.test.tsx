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
import { cleanup, renderHook } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { checkIsActive } from '@/components/layout/lib/url-utils'
import type { NavGroup } from '@/components/layout/types'
import {
  parseSidebarModulesAdmin,
  serializeSidebarModulesAdmin,
} from '@/features/system-settings/maintenance/config'
import { useAuthStore } from '@/stores/auth-store'

import {
  useSidebarConfig,
  useIsSidebarModuleVisible,
} from '../use-sidebar-config'
import { useSidebarData } from '../use-sidebar-data'

beforeEach(() =>
  vi.stubGlobal('localStorage', {
    getItem: () => null,
    setItem: () => undefined,
    removeItem: () => undefined,
  })
)
afterEach(() => {
  cleanup()
  vi.unstubAllGlobals()
  useAuthStore.getState().auth.reset()
})

function sidebarFor(admin?: object, user?: object, canConfigure = true) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  client.setQueryData(['status'], {
    SidebarModulesAdmin: admin ? JSON.stringify(admin) : '',
  })
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'alice',
    role: 1,
    permissions: { sidebar_settings: canConfigure },
    sidebar_modules: user ? JSON.stringify(user) : '',
  })
  function Wrapper(props: { children: ReactNode }) {
    return (
      <QueryClientProvider client={client}>
        {props.children}
      </QueryClientProvider>
    )
  }
  return renderHook(
    () => ({
      groups: useSidebarConfig(useSidebarData().navGroups),
      security: useIsSidebarModuleVisible('/security'),
      profile: useIsSidebarModuleVisible('/profile'),
      wallet: useIsSidebarModuleVisible('/wallet'),
      media: useIsSidebarModuleVisible('/media-management'),
    }),
    { wrapper: Wrapper }
  )
}
function links(groups: NavGroup[]) {
  return groups.flatMap((group) =>
    group.items.flatMap(
      (item) =>
        item.items ??
        (item.type === 'chat-presets' ? (item.links ?? []) : [item])
    )
  )
}

describe('business navigation compatibility', () => {
  it('keeps account controls in the avatar menu and removes their duplicate sidebar entries', () => {
    const { result } = sidebarFor(
      { personal: { enabled: true, personal: true, topup: true } },
      { personal: { enabled: true, personal: true } }
    )
    expect(result.current.security).toBe(true)
    expect(result.current.profile).toBe(true)
    expect(result.current.wallet).toBe(true)
    expect(links(result.current.groups).map((item) => item.url)).not.toEqual(
      expect.arrayContaining(['/profile', '/security', '/wallet'])
    )
    expect(
      links(result.current.groups).some((item) => item.url === '/keys')
    ).toBe(true)
  })
  it.each([
    [{ personal: { enabled: true, security: false } }, undefined],
    [{ personal: { enabled: false } }, { personal: { security: true } }],
    [undefined, { personal: { enabled: true, security: false } }],
    [undefined, { personal: { enabled: false } }],
  ])(
    'admin and personal disablement also controls avatar links (%j, %j)',
    (admin, user) => {
      expect(sidebarFor(admin, user).result.current.security).toBe(false)
    }
  )
  it('ignores stale personal configuration when the account cannot edit sidebar settings', () => {
    const { result } = sidebarFor(
      undefined,
      { personal: { security: false }, finance: { model_profits: false } },
      false
    )
    expect(result.current.security).toBe(true)
    expect(
      links(result.current.groups).some((item) => item.url === '/model-profits')
    ).toBe(true)
  })
  it('does not bind new finance modules to the historical system settings toggle', () => {
    const { result } = sidebarFor({ admin: { enabled: true, setting: false } })
    expect(
      links(result.current.groups).some((item) => item.url === '/model-profits')
    ).toBe(true)
    expect(
      links(result.current.groups).some(
        (item) => item.url === '/system-settings/site'
      )
    ).toBe(false)
  })
  it.each([
    [
      { media: { enabled: true, operations: true, video_usage: false } },
      undefined,
      true,
    ],
    [
      { media: { enabled: true, operations: false, video_usage: true } },
      undefined,
      true,
    ],
    [
      { media: { enabled: true, operations: false, video_usage: false } },
      undefined,
      false,
    ],
    [{ media: { enabled: false } }, { media: { operations: true } }, false],
    [
      undefined,
      { media: { enabled: true, operations: false, video_usage: true } },
      true,
    ],
    [
      undefined,
      { media: { enabled: true, operations: true, video_usage: false } },
      true,
    ],
    [
      undefined,
      { media: { enabled: true, operations: false, video_usage: false } },
      false,
    ],
    [undefined, { media: { enabled: false } }, false],
    [
      { media: { operations: false, video_usage: true } },
      { media: { operations: true, video_usage: false } },
      false,
    ],
  ])(
    'combines historical media preferences without reopening a disabled module (%j, %j)',
    (admin, user, visible) => {
      const { result } = sidebarFor(admin, user)
      expect(result.current.media).toBe(visible)
      expect(
        links(result.current.groups).some(
          (item) => item.url === '/video-management'
        )
      ).toBe(visible)
    }
  )
  it('keeps historical image/video routes active under the matching replacement entries', () => {
    const { result } = sidebarFor()
    const media = links(result.current.groups).find(
      (item) => item.url === '/video-management'
    )
    const images = links(result.current.groups).find(
      (item) => item.url === '/image-management'
    )
    expect(images && checkIsActive('/media-operations', images)).toBe(true)
    expect(media && checkIsActive('/video-usage', media)).toBe(true)
    expect(media && checkIsActive('/media-management', media)).toBe(true)
  })
  it('retains legacy drawing visibility and active state inside the diagnostics parent', () => {
    const { result } = sidebarFor({
      console: { enabled: true, task: false, midjourney: true },
    })
    const tasks = result.current.groups
      .flatMap((group) => group.items)
      .find((item) => item.title === 'Tasks & diagnostics')
    expect(
      tasks?.items?.find((item) => item.url === '/usage-logs/task')
    ).toBeDefined()
    expect(
      tasks && checkIsActive('/usage-logs/drawing?type=image', tasks)
    ).toBe(true)
  })
  it('hides the consolidated task link only when both historical task modules are disabled', () => {
    const { result } = sidebarFor({
      console: { enabled: true, task: false, midjourney: false },
    })
    expect(
      links(result.current.groups).some(
        (item) => item.url === '/usage-logs/task'
      )
    ).toBe(false)
  })
  it('keeps audit independent when call logs are disabled', () => {
    const config = parseSidebarModulesAdmin(
      '{"console":{"enabled":true,"log":false}}'
    )
    expect(config.console.audit).toBe(true)
    const { result } = sidebarFor(config)
    expect(
      links(result.current.groups).some(
        (item) => item.url === '/usage-logs/common'
      )
    ).toBe(false)
    expect(
      links(result.current.groups).some(
        (item) => item.url === '/usage-logs/audit'
      )
    ).toBe(true)
  })
  it('applies platform module restrictions before personal preferences can narrow them', () => {
    const { result } = sidebarFor(
      { finance: { enabled: true, model_profits: false } },
      { finance: { enabled: true, model_profits: true } }
    )
    expect(
      links(result.current.groups).some((item) => item.url === '/model-profits')
    ).toBe(false)
    expect(
      links(result.current.groups).some(
        (item) => item.url === '/balance-ledger'
      )
    ).toBe(true)
  })
  it('migrates an explicitly closed legacy admin section without reopening new business groups', () => {
    const { result } = sidebarFor({ admin: { enabled: false } })
    expect(
      links(result.current.groups).some((item) => item.url === '/model-profits')
    ).toBe(false)
    expect(
      links(result.current.groups).some((item) => item.url === '/channels')
    ).toBe(false)
    expect(
      result.current.groups.find((group) => group.id === 'configuration')
    ).toBeUndefined()
  })
  it('round-trips independent business toggles and preserves unknown historical fields', () => {
    const config = parseSidebarModulesAdmin(
      '{"admin":{"enabled":true,"channel":false,"future":false},"finance":{"enabled":true,"model_profits":false},"future":{"enabled":false,"extra":true}}'
    )
    const saved = parseSidebarModulesAdmin(serializeSidebarModulesAdmin(config))
    expect(saved.models.channel).toBe(false)
    expect(saved.finance.model_profits).toBe(false)
    expect(saved.admin.future).toBe(false)
    expect(saved.future).toEqual({ enabled: false, extra: true })
  })
  it('shows the AI playground when chat presets are disabled without exposing the presets', () => {
    const { result } = sidebarFor({
      chat: { enabled: true, chat: false, playground: true },
    })
    const chat = result.current.groups
      .flatMap((group) => group.items)
      .find((item) => item.type === 'chat-presets')
    expect(chat?.type === 'chat-presets' && chat.showPresets).toBe(false)
    expect(
      chat?.type === 'chat-presets' && chat.links?.map((item) => item.url)
    ).toEqual(['/playground'])
  })
})

it('preserves the legacy call-log switch for API errors until an explicit independent override is saved', () => {
  const hidden = sidebarFor({ console: { enabled: true, log: false } })
  expect(
    links(hidden.result.current.groups).some(
      (item) => item.url === '/usage-logs/errors'
    )
  ).toBe(false)
  hidden.unmount()
  const shown = sidebarFor({
    console: { enabled: true, log: false, errors: true },
  })
  expect(
    links(shown.result.current.groups).some(
      (item) => item.url === '/usage-logs/errors'
    )
  ).toBe(true)
})
it('retains one operations entry when only the upstream recharge module is enabled', () => {
  const { result } = sidebarFor({
    finance: { enabled: true, operations: false, upstream_topups: true },
  })
  const items = links(result.current.groups)
  expect(items.some((item) => item.url === '/usage-logs/operations')).toBe(true)
  expect(items.some((item) => item.url === '/upstream-topups')).toBe(false)
})
