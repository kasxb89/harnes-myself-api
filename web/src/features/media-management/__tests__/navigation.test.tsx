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
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { toast } from 'sonner'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import { mediaPayload } from '@/features/media-operations/__tests__/fixtures'
import { videoPayload } from '@/features/video-usage/__tests__/fixtures'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { Route as ImageRoute } from '@/routes/_authenticated/image-management'
import { Route as ManagementRoute } from '@/routes/_authenticated/media-management'
import { Route as OperationsRoute } from '@/routes/_authenticated/media-operations'
import { Route as VideoManagementRoute } from '@/routes/_authenticated/video-management'
import { Route as VideoRoute } from '@/routes/_authenticated/video-usage'
import { useAuthStore } from '@/stores/auth-store'

import { MediaManagementPage } from '..'
import {
  mediaManagementSearchSchema,
  generationManagementSearchSchema,
} from '../search'

const i18n = createInstance()
const clients: QueryClient[] = []
const concurrency = {
  schema_version: 1,
  revision: 'current',
  defaults: { video: 4, image: 10 },
  pool_overrides: [],
  pools: [],
}
function setPermissions(media: boolean, video: boolean, id = 1) {
  useAuthStore.getState().auth.setUser({
    id,
    username: `admin-${id}`,
    role: ROLE.ADMIN,
    permissions: {
      admin_permissions: {
        media_operations: { read: media },
        video_usage: { read: video },
      },
    },
  })
}
function mockReports() {
  return vi.spyOn(api, 'get').mockImplementation(async (url) => {
    let data: unknown = concurrency
    if (url === '/api/media-operations') data = mediaPayload
    if (url === '/api/video-usage') data = videoPayload
    return { data: { success: true, data } }
  })
}
async function confirmConcurrencyAction(action: 'save' | 'reconcile') {
  const input = await screen.findByLabelText(
    'Default video concurrency per key'
  )
  if (action === 'save') fireEvent.change(input, { target: { value: '6' } })
  await userEvent.click(
    screen.getByRole('button', {
      name:
        action === 'save'
          ? 'Save account concurrency'
          : 'Reconcile account pools',
    })
  )
  await userEvent.click(
    within(screen.getByRole('alertdialog')).getByRole('button', {
      name: 'Confirm',
    })
  )
}
async function renderPage(path = '/media-management?tab=tasks') {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  const root = createRootRoute()
  let kind: 'image' | 'video' | undefined
  if (path.startsWith('/image-management')) kind = 'image'
  if (path.startsWith('/video-management')) kind = 'video'
  let routePath = '/media-management'
  if (kind === 'image') routePath = '/image-management'
  if (kind === 'video') routePath = '/video-management'
  const page = createRoute({
    getParentRoute: () => root,
    path: routePath,
    validateSearch: kind
      ? generationManagementSearchSchema
      : mediaManagementSearchSchema,
    component: () => <MediaManagementPage kind={kind} />,
  })
  const router = createRouter({
    routeTree: root.addChildren([page]),
    history: createMemoryHistory({ initialEntries: [path] }),
  })
  await router.load()
  const view = render(
    <I18nextProvider i18n={i18n}>
      <QueryClientProvider client={client}>
        <RouterProvider router={router} />
      </QueryClientProvider>
    </I18nextProvider>
  )
  return { ...view, router }
}
beforeEach(async () => {
  await i18n.init({
    lng: 'en',
    fallbackLng: 'en',
    keySeparator: false,
    nsSeparator: false,
  })
  setPermissions(true, true)
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
})
afterEach(() => {
  cleanup()
  clients.splice(0).forEach((client) => client.clear())
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
})

it('the merged entry has exactly three main tabs and loads only the selected report', async () => {
  const get = mockReports()
  const { router } = await renderPage()
  await screen.findByRole('table', { name: 'Current task records' })
  const sections = screen.getByRole('tablist', {
    name: 'Image and video management sections',
  })
  expect(within(sections).getAllByRole('tab')).toHaveLength(3)
  expect(screen.getAllByRole('heading', { level: 2 })).toHaveLength(1)
  expect(
    screen.getByRole('heading', { name: 'Image and video management' })
  ).toBeVisible()
  expect(
    screen.queryByRole('tab', { name: 'Operational observations' })
  ).not.toBeInTheDocument()
  expect(get.mock.calls.map((call) => call[0])).toEqual([
    '/api/media-operations',
  ])
  await userEvent.click(
    within(sections).getByRole('tab', { name: 'Concurrency settings' })
  )
  await screen.findByRole('region', {
    name: 'Account concurrency configuration',
  })
  await waitFor(() =>
    expect(router.state.location.search).toEqual({ tab: 'concurrency' })
  )
  await waitFor(() =>
    expect(get).toHaveBeenCalledWith(
      '/api/media-concurrency',
      expect.objectContaining({
        signal: expect.any(AbortSignal),
        disableDuplicate: true,
      })
    )
  )
  expect(get.mock.calls.map((call) => call[0])).not.toContain(
    '/api/video-usage'
  )
  expect(
    screen.queryByRole('button', { name: 'Save account concurrency' })
  ).not.toBeInTheDocument()
})

it('a video-only administrator safely falls back from inaccessible or invalid tabs without media requests', async () => {
  setPermissions(false, true)
  const get = mockReports()
  const { router } = await renderPage('/media-management?tab=concurrency')
  await screen.findByRole('table', { name: 'Current video task records' })
  expect(
    screen.queryByRole('tab', { name: 'Task monitoring' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('tab', { name: 'Concurrency settings' })
  ).not.toBeInTheDocument()
  await waitFor(() =>
    expect(router.state.location.search).toEqual({ tab: 'video' })
  )
  await act(async () => {
    await router.navigate({
      to: '/media-management',
      search: { tab: 'invalid' },
    } as never)
  })
  expect(screen.getByRole('tab', { name: 'Video statistics' })).toHaveAttribute(
    'aria-selected',
    'true'
  )
  expect(get.mock.calls.map((call) => call[0])).toEqual(['/api/video-usage'])
})

it('a media-only administrator falls back from video and never requests video data', async () => {
  setPermissions(true, false)
  const get = mockReports()
  const { router } = await renderPage('/media-management?tab=video')
  await screen.findByRole('table', { name: 'Current task records' })
  expect(
    screen.queryByRole('tab', { name: 'Video statistics' })
  ).not.toBeInTheDocument()
  await waitFor(() =>
    expect(router.state.location.search).toEqual({ tab: 'tasks' })
  )
  expect(get.mock.calls.map((call) => call[0])).toEqual([
    '/api/media-operations',
  ])
})

it('history navigation and reload preserve the selected main tab and internal report view', async () => {
  mockReports()
  const { router, unmount } = await renderPage()
  await screen.findByRole('table', { name: 'Current task records' })
  await userEvent.click(
    screen.getByRole('tab', { name: 'Historical observations' })
  )
  await userEvent.click(screen.getByRole('tab', { name: 'Video statistics' }))
  await screen.findByRole('table', { name: 'Current video task records' })
  await userEvent.click(screen.getByRole('tab', { name: 'Task details' }))
  await act(async () => {
    router.history.back()
  })
  await waitFor(() =>
    expect(
      screen.getByRole('tab', { name: 'Task monitoring' })
    ).toHaveAttribute('aria-selected', 'true')
  )
  expect(
    screen.getByRole('tab', { name: 'Historical observations' })
  ).toHaveAttribute('aria-selected', 'true')
  await act(async () => {
    router.history.forward()
  })
  await waitFor(() =>
    expect(
      screen.getByRole('tab', { name: 'Video statistics' })
    ).toHaveAttribute('aria-selected', 'true')
  )
  expect(screen.getByRole('tab', { name: 'Task details' })).toHaveAttribute(
    'aria-selected',
    'true'
  )
  const savedLink = router.state.location.href
  unmount()
  await renderPage(savedLink)
  await screen.findByRole('table', { name: 'Current video task records' })
  expect(screen.getByRole('tab', { name: 'Video statistics' })).toHaveAttribute(
    'aria-selected',
    'true'
  )
})

it('revoking the visible permission hides its cached content and switching accounts clears report state', async () => {
  const get = mockReports()
  await renderPage('/media-management?tab=video')
  await screen.findByRole('table', { name: 'Current video task records' })
  await userEvent.click(screen.getByRole('tab', { name: 'Task details' }))
  await act(async () => {
    setPermissions(true, false)
  })
  await screen.findByRole('table', { name: 'Current task records' })
  expect(
    screen.queryByRole('tab', { name: 'Video statistics' })
  ).not.toBeInTheDocument()
  expect(screen.queryByText('task-101')).not.toBeInTheDocument()
  await act(async () => {
    setPermissions(false, false)
  })
  expect(screen.getByText('Access denied')).toBeVisible()
  const requests = get.mock.calls.length
  await act(async () => {
    setPermissions(false, true, 22)
  })
  await screen.findByRole('table', { name: 'Current video task records' })
  expect(
    screen.getByRole('tab', { name: 'Current task records' })
  ).toHaveAttribute('aria-selected', 'true')
  expect(get.mock.calls.length).toBe(requests + 1)
})

it('no read permissions deny all content and requests even with a saved tab link', async () => {
  setPermissions(false, false)
  const get = mockReports()
  await renderPage('/media-management?tab=video')
  expect(screen.getByText('Access denied')).toBeVisible()
  expect(get).not.toHaveBeenCalled()
})

it('main tabs remain keyboard accessible and wrap on narrow screens', async () => {
  mockReports()
  await renderPage()
  await screen.findByRole('table', { name: 'Current task records' })
  const sections = screen.getByRole('tablist', {
    name: 'Image and video management sections',
  })
  expect(sections).toHaveClass('flex-wrap')
  within(sections).getByRole('tab', { name: 'Task monitoring' }).focus()
  await userEvent.keyboard('{End}{Enter}')
  await screen.findByRole('table', { name: 'Current video task records' })
  expect(
    within(sections).getByRole('tab', { name: 'Video statistics' })
  ).toHaveAttribute('aria-selected', 'true')
})

it('legacy links redirect to the matching canonical tab after checking their original permissions', async () => {
  await expect(async () =>
    OperationsRoute.options.beforeLoad?.({
      search: { tab: 'concurrency' },
    } as never)
  ).rejects.toMatchObject({
    options: {
      to: '/media-management',
      search: { tab: 'concurrency' },
      replace: true,
    },
  })
  await expect(async () =>
    VideoRoute.options.beforeLoad?.({} as never)
  ).rejects.toMatchObject({
    options: {
      to: '/media-management',
      search: { tab: 'video' },
      replace: true,
    },
  })
  setPermissions(false, true)
  await expect(async () =>
    OperationsRoute.options.beforeLoad?.({ search: {} } as never)
  ).rejects.toMatchObject({ options: { to: '/403' } })
  setPermissions(true, false)
  await expect(async () =>
    VideoRoute.options.beforeLoad?.({} as never)
  ).rejects.toMatchObject({ options: { to: '/403' } })
})

it('the canonical route resolves an unauthorized saved tab before mounting and denies missing read permissions', async () => {
  setPermissions(false, true)
  await expect(async () =>
    ManagementRoute.options.beforeLoad?.({ search: { tab: 'tasks' } } as never)
  ).rejects.toMatchObject({
    options: { to: '/media-management', search: { tab: 'video' } },
  })
  expect(() =>
    ManagementRoute.options.beforeLoad?.({ search: { tab: 'video' } } as never)
  ).not.toThrow()
  setPermissions(false, false)
  await expect(async () =>
    ManagementRoute.options.beforeLoad?.({ search: { tab: 'video' } } as never)
  ).rejects.toMatchObject({ options: { to: '/403' } })
})

it('restoring a revoked permission waits for fresh data without exposing the previous cached report', async () => {
  const get = mockReports()
  await renderPage('/media-management?tab=video')
  await screen.findByRole('table', { name: 'Current video task records' })
  await act(async () => {
    setPermissions(false, false)
  })
  expect(screen.getByText('Access denied')).toBeVisible()
  get.mockImplementation(() => new Promise(() => {}))
  await act(async () => {
    setPermissions(false, true)
  })
  await screen.findByRole('status', { name: 'Loading video usage' })
  expect(
    screen.queryByRole('table', { name: 'Current video task records' })
  ).not.toBeInTheDocument()
  expect(screen.queryByText('MiniMax-H3')).not.toBeInTheDocument()
})

it.each(['save', 'reconcile'] as const)(
  'a late %s response after read revocation cannot recreate cached configuration before read access returns',
  async (action) => {
    useAuthStore
      .getState()
      .auth.setUser({ id: 1, username: 'root', role: ROLE.SUPER_ADMIN })
    const get = mockReports()
    const lateResponse = {
      data: {
        success: true,
        data: {
          ...concurrency,
          revision: 'late-result',
          defaults: { video: 9, image: 10 },
        },
      },
    }
    let complete: (value: typeof lateResponse) => void = () => undefined
    const pending = new Promise<typeof lateResponse>((resolve) => {
      complete = resolve
    })
    const write = vi
      .spyOn(api, action === 'save' ? 'put' : 'post')
      .mockImplementation(() => pending)
    await renderPage('/media-management?tab=concurrency')
    await confirmConcurrencyAction(action)
    expect(write).toHaveBeenCalledOnce()
    await act(async () => {
      setPermissions(false, false)
    })
    expect(screen.getByText('Access denied')).toBeVisible()
    await act(async () => {
      complete(lateResponse)
      await pending
    })
    get.mockClear()
    get.mockResolvedValue({
      data: {
        success: true,
        data: {
          ...concurrency,
          revision: 'fresh-read',
          defaults: { video: 5, image: 10 },
        },
      },
    })
    await act(async () => {
      setPermissions(true, true)
    })
    await waitFor(() =>
      expect(
        screen.getByLabelText('Default video concurrency per key')
      ).toHaveValue('5')
    )
    expect(get).toHaveBeenCalledOnce()
    expect(
      screen.queryByText('Account concurrency saved')
    ).not.toBeInTheDocument()
    expect(
      screen.queryByText('Account pools reconciled')
    ).not.toBeInTheDocument()
  }
)

it.each(['save', 'reconcile'] as const)(
  'a late %s response from an earlier account cannot populate that account after switching back',
  async (action) => {
    useAuthStore
      .getState()
      .auth.setUser({ id: 1, username: 'root', role: ROLE.SUPER_ADMIN })
    const get = mockReports()
    const lateResponse = {
      data: {
        success: true,
        data: {
          ...concurrency,
          revision: 'earlier-account',
          defaults: { video: 9, image: 10 },
        },
      },
    }
    let complete: (value: typeof lateResponse) => void = () => undefined
    const pending = new Promise<typeof lateResponse>((resolve) => {
      complete = resolve
    })
    const write = vi
      .spyOn(api, action === 'save' ? 'put' : 'post')
      .mockImplementation(() => pending)
    await renderPage('/media-management?tab=concurrency')
    await confirmConcurrencyAction(action)
    expect(write).toHaveBeenCalledOnce()
    await act(async () => {
      setPermissions(true, true, 22)
    })
    await waitFor(() =>
      expect(
        screen.getByLabelText('Default video concurrency per key')
      ).toHaveValue('4')
    )
    await act(async () => {
      complete(lateResponse)
      await pending
    })
    get.mockClear()
    get.mockResolvedValue({
      data: {
        success: true,
        data: {
          ...concurrency,
          revision: 'fresh-returning-account',
          defaults: { video: 5, image: 10 },
        },
      },
    })
    await act(async () => {
      setPermissions(true, true, 1)
    })
    await waitFor(() =>
      expect(
        screen.getByLabelText('Default video concurrency per key')
      ).toHaveValue('5')
    )
    expect(get).toHaveBeenCalledOnce()
  }
)

it.each([
  ['save', false],
  ['save', true],
  ['reconcile', false],
  ['reconcile', true],
] as const)(
  'a late %s response after root is downgraded cannot change the mounted configuration (root restored before response: %s)',
  async (action, restoreRootBeforeResponse) => {
    useAuthStore
      .getState()
      .auth.setUser({ id: 1, username: 'root', role: ROLE.SUPER_ADMIN })
    mockReports()
    const lateResponse = {
      data: {
        success: true,
        data: {
          ...concurrency,
          revision: 'lost-root-action',
          defaults: { video: 9, image: 10 },
        },
      },
    }
    let complete: (value: typeof lateResponse) => void = () => undefined
    const pending = new Promise<typeof lateResponse>((resolve) => {
      complete = resolve
    })
    const write = vi
      .spyOn(api, action === 'save' ? 'put' : 'post')
      .mockImplementation(() => pending)
    await renderPage('/media-management?tab=concurrency')
    await confirmConcurrencyAction(action)
    expect(write).toHaveBeenCalledOnce()
    await act(async () => {
      setPermissions(true, true)
    })
    expect(
      screen.getByLabelText('Default video concurrency per key')
    ).toBeDisabled()
    if (restoreRootBeforeResponse) {
      await act(async () => {
        useAuthStore
          .getState()
          .auth.setUser({ id: 1, username: 'root', role: ROLE.SUPER_ADMIN })
      })
    }
    await act(async () => {
      complete(lateResponse)
      await pending
    })
    expect(
      screen.getByLabelText('Default video concurrency per key')
    ).toHaveValue('4')
    expect(
      screen.queryByText('Account concurrency saved')
    ).not.toBeInTheDocument()
    expect(
      screen.queryByText('Account pools reconciled')
    ).not.toBeInTheDocument()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    await act(async () => {
      useAuthStore
        .getState()
        .auth.setUser({ id: 1, username: 'root', role: ROLE.SUPER_ADMIN })
    })
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(
      screen.getByLabelText('Default video concurrency per key')
    ).toHaveValue('4')
  }
)

it.each(['save', 'reconcile'] as const)(
  'a late %s failure after root authority changes does not notify the current session',
  async (action) => {
    useAuthStore
      .getState()
      .auth.setUser({ id: 1, username: 'root', role: ROLE.SUPER_ADMIN })
    mockReports()
    let fail: (reason: Error) => void = () => undefined
    const pending = new Promise<never>((_resolve, reject) => {
      fail = reject
    })
    const write = vi
      .spyOn(api, action === 'save' ? 'put' : 'post')
      .mockImplementation(() => pending)
    const errorToast = vi.spyOn(toast, 'error')
    await renderPage('/media-management?tab=concurrency')
    await confirmConcurrencyAction(action)
    expect(write).toHaveBeenCalledOnce()
    await act(async () => {
      setPermissions(true, true)
    })
    await act(async () => {
      useAuthStore
        .getState()
        .auth.setUser({ id: 1, username: 'root', role: ROLE.SUPER_ADMIN })
    })
    await act(async () => {
      fail(new Error('earlier authorization operation failed'))
      await pending.catch(() => undefined)
    })
    expect(
      screen.queryByText('Failed to save account concurrency')
    ).not.toBeInTheDocument()
    expect(
      screen.queryByText('Failed to reconcile account pools')
    ).not.toBeInTheDocument()
    expect(errorToast).not.toHaveBeenCalled()
    expect(screen.queryByRole('alertdialog')).not.toBeInTheDocument()
    expect(
      screen.getByLabelText('Default video concurrency per key')
    ).toHaveValue('4')
  }
)

it.each(['image', 'video'] as const)(
  'the %s task entry fixes the server filter and hides the cross-type selector',
  async (kind) => {
    const get = mockReports()
    await renderPage(`/${kind}-management?tab=tasks`)
    await screen.findByRole('table', { name: 'Current task records' })
    expect(
      screen.getByRole('heading', {
        name: kind === 'image' ? 'Image management' : 'Video management',
      })
    ).toBeVisible()
    expect(
      screen.queryByRole('combobox', { name: 'Task type' })
    ).not.toBeInTheDocument()
    expect(get).toHaveBeenCalledWith(
      '/api/media-operations',
      expect.objectContaining({
        params: expect.objectContaining({ media_kind: kind }),
      })
    )
    expect(
      screen.queryByRole('tab', { name: 'Video statistics' }) !== null
    ).toBe(kind === 'video')
    await userEvent.click(screen.getByRole('button', { name: 'Reset' }))
    await userEvent.click(screen.getByRole('button', { name: 'Apply filters' }))
    expect(
      get.mock.calls
        .filter((call) => call[0] === '/api/media-operations')
        .every((call) => call[1]?.params?.media_kind === kind)
    ).toBe(true)
  }
)
it('the new video entry gives a video-only administrator statistics without requesting task or concurrency APIs', async () => {
  setPermissions(false, true)
  const get = mockReports()
  const { router } = await renderPage('/video-management?tab=tasks')
  await screen.findByRole('table', { name: 'Current video task records' })
  await waitFor(() =>
    expect(router.state.location.search).toEqual({ tab: 'statistics' })
  )
  expect(get.mock.calls.map((call) => call[0])).toEqual(['/api/video-usage'])
  expect(
    screen.queryByRole('tab', { name: 'Concurrency settings' })
  ).not.toBeInTheDocument()
})
it('new image and video route guards preserve independently granted historical capabilities', async () => {
  setPermissions(false, true)
  await expect(async () =>
    ImageRoute.options.beforeLoad?.({ search: { tab: 'tasks' } } as never)
  ).rejects.toMatchObject({ options: { to: '/403' } })
  await expect(async () =>
    VideoManagementRoute.options.beforeLoad?.({
      search: { tab: 'tasks' },
    } as never)
  ).rejects.toMatchObject({
    options: { to: '/video-management', search: { tab: 'statistics' } },
  })
  setPermissions(true, false)
  await expect(async () =>
    ImageRoute.options.beforeLoad?.({ search: { tab: 'statistics' } } as never)
  ).rejects.toMatchObject({
    options: { to: '/image-management', search: { tab: 'tasks' } },
  })
})
