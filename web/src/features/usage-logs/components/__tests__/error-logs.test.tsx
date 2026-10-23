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
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import i18next from 'i18next'
import { afterEach, expect, it, vi } from 'vitest'

import zh from '@/i18n/locales/zh.json'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { Route as usageLogsRoute } from '@/routes/_authenticated/usage-logs/$section'
import { useAuthStore } from '@/stores/auth-store'

import { usageLogSchema } from '../../data/schema'
import { fetchLogsByCategory } from '../../lib/utils'
import { UsageLogsProvider } from '../usage-logs-provider'
import { UsageLogsTable } from '../usage-logs-table'

// Chart rendering is outside this route and log-table contract.
vi.mock('@visactor/react-vchart', () => ({ VChart: () => null }))

const reason = `Upstream rejected reasoning_content. ${'Detailed diagnostic context. '.repeat(80)}`
const record = usageLogSchema.parse({
  id: 1,
  user_id: 42,
  username: 'alice',
  created_at: 1792380605,
  type: 5,
  content: reason,
  model_name: 'deepseek-flash',
  channel: 16,
  channel_name: 'Production',
  request_id: 'req-diagnostic-123',
  other: JSON.stringify({
    status_code: 400,
    error_code: 'invalid_request_error',
  }),
})

const h3Message = 'H3 积分余额不足 (1008)'
const h3Content = `status_code=402, ${JSON.stringify({
  type: 'error',
  error: {
    type: 'insufficient_balance_error',
    message: h3Message,
    http_code: '402',
  },
  request_id: 'upstream-h3-request',
})}`
const h3Record = usageLogSchema.parse({
  ...record,
  content: h3Content,
  model_name: 'MiniMax-H3',
  other: JSON.stringify({
    status_code: 402,
    error_type: 'openai_error',
    error_code: 'bad_response_status_code',
  }),
})

it.each([false, true])(
  'separates mapped task status from the observed upstream response (mobile=%s)',
  async (mobile) => {
    const entry = usageLogSchema.parse({
      ...record,
      content: 'status_code=502, Task failed',
      other: JSON.stringify({
        status_code: 502,
        error_type: 'task_error',
        error_code: 'task_failed',
        status_code_origin: 'gateway_task_mapping',
        upstream_http_status: 200,
        provider_error_code: 'content_policy_violation',
      }),
    })
    await renderErrors(false, mobile, entry)
    await waitFor(() =>
      expect(
        within(screen.getByTitle('Click to view full details')).getByText(
          'Content safety policy triggered'
        )
      ).toBeVisible()
    )
    await userEvent.click(screen.getByTitle('Click to view full details'))
    const details = within(await screen.findByRole('dialog'))
    expect(details.getByText('Gateway task status')).toBeVisible()
    expect(details.getByText('Upstream HTTP Status')).toBeVisible()
    expect(details.getByText('502', { exact: true })).toBeVisible()
    expect(details.getByText('200', { exact: true })).toBeVisible()
    expect(details.getByText('Content safety policy triggered')).toBeVisible()
  }
)

async function renderErrors(empty = false, mobile = false, entry = record) {
  if (mobile) {
    const matchMedia = window.matchMedia
    vi.spyOn(window, 'matchMedia').mockImplementation((query) => ({
      ...matchMedia(query),
      matches:
        query === '(max-width: 640px)' ||
        query.includes('prefers-reduced-motion'),
    }))
  }
  useAuthStore.getState().auth.setUser({
    id: 1,
    username: 'admin',
    role: ROLE.ADMIN,
    permissions: { admin_permissions: { diagnostics: { read: true } } },
  })
  const get = vi.spyOn(api, 'get').mockImplementation(async (url) => ({
    data: {
      success: true,
      data: url.startsWith('/api/log?')
        ? { items: empty ? [] : [entry], total: empty ? 0 : 1 }
        : [],
    },
  }))
  const root = createRootRoute()
  const auth = createRoute({ getParentRoute: () => root, id: '_authenticated' })
  const logs = createRoute({
    getParentRoute: () => auth,
    path: '/usage-logs/$section',
    validateSearch: (search: Record<string, unknown>) => search,
    component: () => (
      <UsageLogsProvider>
        <UsageLogsTable logCategory='common' errorOnly />
      </UsageLogsProvider>
    ),
  })
  const router = createRouter({
    routeTree: root.addChildren([auth.addChildren([logs])]),
    history: createMemoryHistory({
      initialEntries: ['/usage-logs/errors?type=2'],
    }),
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  if (mobile) {
    await screen.findByRole('button', { name: 'Filter' })
  } else {
    await screen.findByPlaceholderText('Model Name')
  }
  return { router, get }
}

afterEach(async () => {
  cleanup()
  await i18next.changeLanguage('en')
  vi.restoreAllMocks()
  useAuthStore.getState().auth.reset()
})

it.each([
  ['desktop', false],
  ['mobile', true],
] as const)(
  'shows the H3 upstream balance cause on the %s preview and preserves the full response in details',
  async (_surface, mobile) => {
    await renderErrors(false, mobile, h3Record)
    await waitFor(() => {
      const preview = within(screen.getByTitle('Click to view full details'))
      expect(
        preview.getByText('Upstream account balance insufficient')
      ).toBeVisible()
      expect(preview.getByText(h3Message)).toBeVisible()
    })
    await userEvent.click(screen.getByTitle('Click to view full details'))
    const details = within(await screen.findByRole('dialog'))
    expect(details.getByText('Failure Reason')).toBeVisible()
    expect(details.getByText(h3Message)).toBeVisible()
    expect(details.getByText('insufficient_balance_error')).toBeVisible()
    expect(details.getByText(h3Content)).toBeVisible()
  }
)

it('shows a different upstream 402 reason without labelling it as insufficient balance', async () => {
  const message = 'This model requires a paid subscription'
  await renderErrors(false, false, {
    ...h3Record,
    content: `status_code=402, ${JSON.stringify({
      error: { type: 'subscription_required', message },
    })}`,
  })
  await waitFor(() =>
    expect(
      within(screen.getByTitle('Click to view full details')).getByText(message)
    ).toBeVisible()
  )
  expect(
    screen.queryByText('Upstream account balance insufficient')
  ).not.toBeInTheDocument()
})

it('translates the diagnosis when changing language while preserving the original upstream reason', async () => {
  i18next.addResourceBundle('zhCN', 'translation', zh.translation)
  await renderErrors(false, false, h3Record)
  await screen.findByTitle('Click to view full details')
  await act(async () => {
    await i18next.changeLanguage('zhCN')
  })
  await userEvent.click(
    await screen.findByTitle(i18next.t('Click to view full details'))
  )
  const details = within(await screen.findByRole('dialog'))
  expect(details.getByText('具体原因')).toBeVisible()
  expect(details.getByText('上游账户余额不足')).toBeVisible()
  expect(details.getByText(h3Message)).toBeVisible()
  expect(details.getByText(h3Content)).toBeVisible()
})

it.each([
  {
    name: 'upstream rate limit',
    content:
      'status_code=429, {"error":{"type":"rate_limit_error","message":"Rate limit exceeded"}}',
    other: {
      error_code: 'bad_response_status_code',
      error_type: 'openai_error',
      status_code: 429,
    },
    title: '触发请求限流',
    source: '上游服务',
  },
  {
    name: 'gateway DNS failure',
    content:
      'status_code=500, dial tcp: lookup api.example.invalid: no such host',
    other: {
      error_code: 'do_request_failed',
      error_type: 'openai_error',
      status_code: 500,
    },
    title: 'DNS 解析失败',
    source: '本地网关',
  },
  {
    name: 'local user quota failure',
    content: 'status_code=403, user quota is not enough',
    other: {
      error_code: 'insufficient_user_quota',
      error_type: 'new_api_error',
      status_code: 403,
    },
    title: '用户余额或套餐额度不足',
    source: '本地网关',
  },
  {
    name: 'HTML error page',
    content:
      '<html><body><img alt="diagnostic-injected" src="invalid">Bad Gateway</body></html>',
    other: {
      error_code: 'bad_response_status_code',
      error_type: 'openai_error',
      status_code: 502,
    },
    title: zh.translation['Empty or unparseable response'],
    source: '来源未确认',
  },
  {
    name: 'unrecognized provider response',
    content:
      'vendor_e938: quote this request identifier when contacting support',
    other: {
      error_code: 'bad_response_status_code',
      error_type: 'openai_error',
      status_code: 520,
    },
    title: '尚未识别具体原因',
    source: '来源未确认',
  },
])(
  'shows Chinese diagnostic and source for $name while preserving the raw response',
  async (fixture) => {
    i18next.addResourceBundle('zhCN', 'translation', zh.translation)
    await renderErrors(false, false, {
      ...record,
      content: fixture.content,
      other: JSON.stringify(fixture.other),
    })
    await screen.findByTitle('Click to view full details')
    await act(async () => {
      await i18next.changeLanguage('zhCN')
    })
    await waitFor(() =>
      expect(
        within(
          screen.getByTitle(i18next.t('Click to view full details'))
        ).getByText(fixture.title)
      ).toBeVisible()
    )
    await userEvent.click(
      screen.getByTitle(i18next.t('Click to view full details'))
    )
    const details = within(await screen.findByRole('dialog'))
    expect(details.getByText(fixture.title)).toBeVisible()
    expect(details.getByText(fixture.source)).toBeVisible()
    expect(details.getByText(fixture.content)).toBeVisible()
    expect(
      details.queryByAltText('diagnostic-injected')
    ).not.toBeInTheDocument()
  }
)

it('shows diagnostics with second precision and timezone while enforcing error-only requests', async () => {
  const { get } = await renderErrors()
  await screen.findByText('deepseek-flash')
  const calls = get.mock.calls.map(([url]) => String(url))
  const logCall = calls.find((url) => url.startsWith('/api/log?')) ?? ''
  expect(new URL(logCall, 'https://test.local').searchParams.get('type')).toBe(
    '5'
  )
  expect(
    screen.queryByRole('combobox', { name: 'Type' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('columnheader', { name: 'Cost' })
  ).not.toBeInTheDocument()
  expect(
    screen.getByText(/\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} [+-]\d{2}:\d{2}/)
  ).toBeVisible()
  expect(screen.getByText('400')).toBeVisible()
  expect(screen.getByText('req-diagnostic-123')).toBeVisible()
  expect(screen.getByText('alice')).toBeVisible()
  await userEvent.click(screen.getByTitle('Click to view full details'))
  expect(
    within(await screen.findByRole('dialog')).getByText(reason.trim())
  ).toBeVisible()
  expect(
    calls.some(
      (url) => url.includes('/stat?') || url.includes('/subscription/')
    )
  ).toBe(false)
})

it('searches and resets user and model filters without leaving the error page', async () => {
  const { router } = await renderErrors()
  await userEvent.type(screen.getByPlaceholderText('Username'), 'alice')
  await userEvent.type(
    screen.getByPlaceholderText('Model Name'),
    'deepseek-flash'
  )
  await userEvent.click(screen.getByRole('button', { name: 'Search' }))
  await waitFor(() =>
    expect(router.state.location.search).toMatchObject({
      username: 'alice',
      model: 'deepseek-flash',
      type: ['5'],
    })
  )
  expect(router.state.location.pathname).toBe('/usage-logs/errors')
  await userEvent.click(screen.getByRole('button', { name: 'Reset' }))
  await waitFor(() =>
    expect(router.state.location.search).toMatchObject({ type: ['5'] })
  )
  expect(router.state.location.pathname).toBe('/usage-logs/errors')
  expect(screen.getByPlaceholderText('Username')).toHaveValue('')
})

it('shows the error-specific empty state when no failures match', async () => {
  await renderErrors(true)
  expect(await screen.findByText('No API errors found')).toBeVisible()
})

it('never reads administrator error records through a non-admin scope', async () => {
  const get = vi.spyOn(api, 'get')
  await expect(
    fetchLogsByCategory({
      logCategory: 'common',
      isAdmin: false,
      errorOnly: true,
      page: 1,
      pageSize: 20,
      searchParams: {},
      columnFilters: [],
    })
  ).rejects.toThrow()
  expect(get).not.toHaveBeenCalled()
})

it('redirects non-admin visitors away from the diagnostics route', async () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 42, username: 'alice', role: ROLE.USER })
  await expect(async () =>
    usageLogsRoute.options.beforeLoad?.({
      params: { section: 'errors' },
      search: {},
    } as never)
  ).rejects.toMatchObject({ options: { to: '/403' } })
})

it('keeps timestamp, status and request identifiers readable on the mobile error card', async () => {
  await renderErrors(false, true)
  await screen.findByText('deepseek-flash')
  expect(
    screen.getByRole('button', { name: /^Time: \d{4}.*[+-]\d{2}:\d{2}$/ })
  ).toBeVisible()
  expect(screen.getByText('HTTP Status: 400')).toBeVisible()
  expect(screen.getByText('Request ID: req-diagnostic-123')).toBeVisible()
  await userEvent.click(screen.getByTitle('Click to view full details'))
  expect(
    within(await screen.findByRole('dialog')).getByText(reason.trim())
  ).toBeVisible()
})
