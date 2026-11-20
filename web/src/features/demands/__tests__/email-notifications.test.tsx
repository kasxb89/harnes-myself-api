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
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import i18next from 'i18next'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import zh from '@/i18n/locales/zh.json'
import { useAuthStore } from '@/stores/auth-store'

import { DemandManagementPage } from '..'
import { parseDemandManagementSearch } from '../demand-search'
import {
  emailDetailURL,
  normalizeRecipients,
  validAdminBaseURL,
  type EmailConfigReply,
  type EmailDelivery,
} from '../email-api'
import { DemandEmailNotifications } from '../email-notifications'

const { get, post, put } = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  put: vi.fn(),
}))
vi.mock('@/lib/api', () => ({ api: { get, post, put } }))
const clients: QueryClient[] = []
const base = '/api/demand-management/email-notifications'
const envelope = (data: unknown) => ({ data: { success: true, data } })
let config: EmailConfigReply
let deliveries: EmailDelivery[]
function view(canWrite = true) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  clients.push(client)
  const root = createRootRoute()
  const page = createRoute({
    getParentRoute: () => root,
    path: '/demand-management',
    validateSearch: parseDemandManagementSearch,
    component: () => (
      <DemandEmailNotifications accountId={10} canWrite={canWrite} />
    ),
  })
  const router = createRouter({
    routeTree: root.addChildren([page]),
    history: createMemoryHistory({
      initialEntries: ['/demand-management?tab=email'],
    }),
  })
  const rendered = render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  return { ...rendered, router }
}
beforeEach(() => {
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
  sessionStorage.clear()
  config = {
    config: {
      revision: 2,
      enabled: false,
      recipients: ['alerts@example.test'],
      types: {
        official_created: true,
        community_pending: true,
        customer_message: true,
      },
      message_merge_seconds: 120,
      admin_base_url: 'https://console.example.test/prefix',
    },
    smtp_ready: true,
  }
  deliveries = []
  get.mockImplementation((url: string) => {
    if (url === `${base}/config`) return Promise.resolve(envelope(config))
    if (url === `${base}/deliveries`) {
      return Promise.resolve(
        envelope({ items: deliveries, next_cursor: '', has_more: false })
      )
    }
    if (url.includes('/test-receipts/')) {
      return Promise.resolve(
        envelope({
          event_id: 'same-event',
          delivery_ids: ['same-delivery'],
          replayed: true,
        })
      )
    }
    return Promise.reject(new Error('Unexpected read'))
  })
  put.mockImplementation((_url, body) =>
    Promise.resolve(
      envelope({ config: { ...body, revision: 3 }, smtp_ready: true })
    )
  )
})
afterEach(() => {
  cleanup()
  clients.splice(0).forEach((client) => client.clear())
  sessionStorage.clear()
  vi.resetAllMocks()
  useAuthStore.getState().auth.reset()
})

test('a disabled empty server configuration stays disabled and does not send on render', async () => {
  config.config.recipients = []
  config.config.admin_base_url = ''
  config.smtp_ready = false
  view()
  expect(
    await screen.findByRole('checkbox', {
      name: 'Enable demand email notifications',
    })
  ).not.toBeChecked()
  expect(screen.getByLabelText('Notification recipients')).toHaveValue('')
  expect(
    screen.getByLabelText('Customer message merge window (seconds)')
  ).toHaveValue(120)
  expect(
    screen.getByRole('button', { name: 'Send one test email per recipient' })
  ).toBeDisabled()
  expect(post).not.toHaveBeenCalled()
  expect(put).not.toHaveBeenCalled()
})

test('saving recipients normalizes duplicates and an enabled configuration requires an address', async () => {
  view()
  const recipients = await screen.findByLabelText('Notification recipients')
  fireEvent.change(recipients, {
    target: { value: ' A@Example.Test\na@example.test\nB@example.test' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Save settings' }))
  await waitFor(() =>
    expect(put).toHaveBeenCalledWith(
      `${base}/config`,
      expect.objectContaining({
        recipients: ['a@example.test', 'b@example.test'],
        revision: 2,
      })
    )
  )
})

test('a CAS failure keeps the draft until an explicit reload of saved settings', async () => {
  put.mockRejectedValue({
    response: { status: 409, data: { code: 'DEMAND_EMAIL_REVISION_CONFLICT' } },
  })
  view()
  const field = await screen.findByLabelText('Notification recipients')
  fireEvent.change(field, { target: { value: 'unsaved@example.test' } })
  fireEvent.click(screen.getByRole('button', { name: 'Save settings' }))
  await screen.findByRole('button', { name: 'Reload saved settings' })
  expect(field).toHaveValue('unsaved@example.test')
  fireEvent.click(screen.getByRole('button', { name: 'Reload saved settings' }))
  await waitFor(() => expect(field).toHaveValue('alerts@example.test'))
  expect(put).toHaveBeenCalledTimes(1)
})

test('a lost test receipt survives remount and checks the same UUID without another POST', async () => {
  post.mockRejectedValueOnce(new Error('Lost response'))
  const first = view()
  fireEvent.click(
    await screen.findByRole('button', {
      name: 'Send one test email per recipient',
    })
  )
  const check = await screen.findByRole('button', {
    name: 'Check original test receipt',
  })
  await waitFor(() => expect(check).toBeEnabled())
  const original = post.mock.calls[0][1]
  expect(original.revision).toBe(2)
  expect(original.client_action_id).toMatch(/^[a-f0-9-]{36}$/)
  first.unmount()
  view()
  fireEvent.click(
    await screen.findByRole('button', { name: 'Check original test receipt' })
  )
  await screen.findByText(
    'Test email queued. Check the sending records for each recipient.'
  )
  expect(get).toHaveBeenCalledWith(
    `${base}/test-receipts/${original.client_action_id}`,
    expect.anything()
  )
  expect(post).toHaveBeenCalledTimes(1)
})

test('unknown delivery shows no resend action or raw SMTP diagnostic and preserves its exact entity link', async () => {
  deliveries = [
    {
      id: 'delivery1',
      domain: 'official',
      entity_id: 'demand-one',
      type: 'customer_message',
      recipient: 'alerts@example.test',
      status: 'unknown',
      number: 'YS-1',
      title: 'Customer project',
      event_count: 2,
      attempt_count: 1,
      due_at: 1,
      created_at: 1700000000,
      updated_at: 1,
      sent_at: 0,
      error_code: 'PRIVATE_RAW_PASSWORD',
      error_stage: 'smtp host secrets',
      pause_reason: '',
      detail_url:
        'https://console.example.test/prefix/demand-management?tab=demands&demandId=demand-one',
    },
  ]
  view(false)
  const table = await screen.findByRole('table')
  expect(within(table).getByText('Delivery unknown')).toBeVisible()
  expect(
    within(table).getByText(
      'The server could not confirm SMTP acceptance. Automatic resend is stopped.'
    )
  ).toBeVisible()
  expect(screen.queryByText('PRIVATE_RAW_PASSWORD')).not.toBeInTheDocument()
  expect(
    within(table).getByRole('link', { name: 'Open demand' })
  ).toHaveAttribute(
    'href',
    '/demand-management?tab=demands&demandId=demand-one'
  )
  expect(
    screen.queryByRole('button', { name: /resend/i })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Save settings' })
  ).not.toBeInTheDocument()
  expect(screen.getByLabelText('Notification recipients')).toBeDisabled()
})

test('an administrator missing either domain read permission cannot see the email tab', async () => {
  useAuthStore.getState().auth.setUser({
    id: 9,
    username: 'community-only',
    role: 10,
    permissions: {
      admin_permissions: {
        demands: { read: false, write: false },
        community_demands: { read: true, write: true },
      },
    },
  })
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <DemandManagementPage search={{ tab: 'email' }} />
    </QueryClientProvider>
  )
  expect(screen.getByText('Access denied')).toBeVisible()
  expect(get).not.toHaveBeenCalled()
})

test('deep links reject conflicting domains or invalid IDs and preserve either exact valid target', () => {
  expect(
    parseDemandManagementSearch({ tab: 'community', orderId: 'order-123' })
  ).toEqual({ tab: 'community', orderId: 'order-123' })
  expect(parseDemandManagementSearch({ demandId: 'demand-123' })).toEqual({
    tab: 'demands',
    demandId: 'demand-123',
  })
  expect(
    parseDemandManagementSearch({ tab: 'community', demandId: 'demand-123' })
  ).toEqual({ invalidLink: true })
  expect(
    parseDemandManagementSearch({
      orderId: '../private',
      demandId: 'demand-123',
    })
  ).toEqual({ invalidLink: true })
})

test('email helpers reject credentials, remote HTTP and mismatched entity links', () => {
  expect(
    normalizeRecipients(' A@Example.test\na@example.test;B@example.test')
  ).toEqual(['a@example.test', 'b@example.test'])
  expect(validAdminBaseURL('http://127.0.0.1:3000/prefix')).toBe(true)
  expect(validAdminBaseURL('https://user:pass@example.test')).toBe(false)
  expect(validAdminBaseURL('http://example.test')).toBe(false)
  const row = {
    domain: 'official',
    entity_id: 'a',
    detail_url: 'https://example.test/demand-management?tab=demands&demandId=b',
  } as EmailDelivery
  expect(emailDetailURL(row)).toBeUndefined()
  expect(
    emailDetailURL({ ...row, detail_url: 'javascript:alert(1)' })
  ).toBeUndefined()
})

test.each([
  'DEMAND_EMAIL_INVALID_INPUT',
  'DEMAND_EMAIL_REVISION_CONFLICT',
  'DEMAND_EMAIL_SMTP_UNAVAILABLE',
])(
  'a confirmed %s pre-enqueue rejection unlocks settings instead of keeping an unknown action',
  async (code) => {
    post.mockRejectedValue({ response: { data: { success: false, code } } })
    view()
    fireEvent.click(
      await screen.findByRole('button', {
        name: 'Send one test email per recipient',
      })
    )
    await screen.findByText(
      'The test was not queued. Reload saved settings before trying again.'
    )
    expect(screen.getByLabelText('Notification recipients')).toBeEnabled()
    expect(
      screen.queryByRole('button', { name: 'Check original test receipt' })
    ).not.toBeInTheDocument()
    expect(
      screen.getByRole('button', { name: 'Reload saved settings' })
    ).toBeEnabled()
    expect(post).toHaveBeenCalledTimes(1)
  }
)

test('an original-key conflict retains receipt-first recovery and does not create a replacement action', async () => {
  post.mockRejectedValue({
    response: {
      data: { success: false, code: 'DEMAND_EMAIL_REQUEST_CONFLICT' },
    },
  })
  view()
  fireEvent.click(
    await screen.findByRole('button', {
      name: 'Send one test email per recipient',
    })
  )
  const button = await screen.findByRole('button', {
    name: 'Check original test receipt',
  })
  await waitFor(() => expect(button).toBeEnabled())
  expect(screen.getByLabelText('Notification recipients')).toBeDisabled()
  expect(
    screen.queryByRole('button', { name: 'Send one test email per recipient' })
  ).not.toBeInTheDocument()
  expect(post).toHaveBeenCalledTimes(1)
})

test('Chinese email settings and validation stay localized', async () => {
  i18next.addResourceBundle('zhCN', 'translation', zh.translation, true, true)
  await i18next.changeLanguage('zhCN')
  try {
    config.config.recipients = []
    view()
    expect(await screen.findByLabelText('收件邮箱')).toBeVisible()
    expect(screen.getByText('邮件发送记录')).toBeVisible()
    fireEvent.click(screen.getByRole('checkbox', { name: '启用需求邮件提醒' }))
    fireEvent.click(
      screen.getByRole('button', { name: zh.translation['Save settings'] })
    )
    expect(await screen.findByText('启用前请至少添加一个收件人')).toBeVisible()
    expect(put).not.toHaveBeenCalled()
    expect(post).not.toHaveBeenCalled()
  } finally {
    await i18next.changeLanguage('en')
  }
})

test('console base validation rejects empty query and fragment delimiters while accepting encoded deployment paths', () => {
  expect(validAdminBaseURL('https://console.example.test?')).toBe(false)
  expect(validAdminBaseURL('https://console.example.test#')).toBe(false)
  expect(validAdminBaseURL('https://console.example.test/path/?')).toBe(false)
  expect(
    validAdminBaseURL(
      'https://console.example.test/team%20space/%E5%90%8E%E5%8F%B0'
    )
  ).toBe(true)
  expect(
    validAdminBaseURL('https://console.example.test/path%3Fname%23part')
  ).toBe(true)
})

test.each([
  {
    domain: 'official',
    tab: 'demands',
    idKey: 'demandId',
    id: 'original-demand',
  },
  {
    domain: 'community',
    tab: 'community',
    idKey: 'orderId',
    id: 'original-order',
  },
] as const)(
  'a $domain record navigates within the current console and returns to email records',
  async ({ domain, tab, idKey, id }) => {
    deliveries = [
      {
        id: 'delivery-local-navigation',
        domain,
        entity_id: id,
        type: 'customer_message',
        recipient: 'alerts@example.test',
        status: 'sent',
        number: 'YS-original',
        title: 'Original request',
        event_count: 1,
        attempt_count: 1,
        due_at: 1,
        created_at: 1700000000,
        updated_at: 1,
        sent_at: 1,
        error_code: '',
        error_stage: '',
        pause_reason: '',
        detail_url: `https://other-console.example.test/prefix/demand-management?tab=${tab}&${idKey}=${id}`,
      },
    ]
    const open = vi.spyOn(window, 'open').mockImplementation(() => null)
    const { router } = view(false)
    const link = await screen.findByRole('link', { name: 'Open demand' })
    expect(link).not.toHaveAttribute('target')
    const rawHref = link.getAttribute('href')
    expect(rawHref).toBeTruthy()
    const href = new URL(rawHref ?? '', window.location.origin)
    expect(href.origin).toBe(window.location.origin)
    expect(href.pathname).toBe('/demand-management')
    expect(Object.fromEntries(href.searchParams)).toEqual({ tab, [idKey]: id })
    fireEvent.click(link)
    await waitFor(() =>
      expect(router.state.location.search).toEqual({ tab, [idKey]: id })
    )
    expect(router.state.location.pathname).toBe('/demand-management')
    expect(open).not.toHaveBeenCalled()
    expect(post).not.toHaveBeenCalled()
    expect(put).not.toHaveBeenCalled()
    router.history.back()
    await waitFor(() =>
      expect(router.state.location.search).toEqual({ tab: 'email' })
    )
    expect(
      await screen.findByRole('link', { name: 'Open demand' })
    ).toBeVisible()
    open.mockRestore()
  }
)
