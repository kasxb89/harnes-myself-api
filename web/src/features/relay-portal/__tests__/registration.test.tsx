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
} from '@testing-library/react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { SignUp } from '@/features/auth/sign-up'
import { beforeLegacyDesignLoad } from '@/routes/(auth)/relay-designs'
import { useAuthStore } from '@/stores/auth-store'

import { parseRelayDesignSearch } from '../search'

const fixture = vi.hoisted(() => ({
  status: {} as Record<string, unknown>,
  register: vi.fn(),
  sendEmailVerification: vi.fn(),
}))
vi.mock('@/features/auth/api', async () => ({
  ...(await vi.importActual<typeof import('@/features/auth/api')>(
    '@/features/auth/api'
  )),
  register: fixture.register,
  sendEmailVerification: fixture.sendEmailVerification,
  wechatLoginByCode: vi.fn(),
}))
vi.mock('@/lib/status-query', () => ({
  statusQueryOptions: {
    queryKey: ['registration-status'],
    queryFn: async () => fixture.status,
  },
  readCachedStatus: () => null,
}))
vi.mock('@/hooks/use-status', () => ({
  useStatus: () => ({ status: fixture.status, loading: false, error: null }),
}))
vi.mock('@/hooks/use-system-config', () => ({
  useSystemConfig: () => ({
    systemName: 'Yunshu AI',
    logo: '/logo.png',
    loading: false,
  }),
}))
vi.mock('../obsidian-object', () => ({
  ObsidianObject: () => <div data-testid='local-3d-renderer-placeholder' />,
}))
const clients: QueryClient[] = []
async function view(initialEntry = '/sign-up?aff=preview-referral') {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  clients.push(client)
  const root = createRootRoute()
  const page = createRoute({
    getParentRoute: () => root,
    path: '/sign-up',
    validateSearch: parseRelayDesignSearch,
    component: SignUp,
  })
  const legacyDesign = createRoute({
    getParentRoute: () => root,
    path: '/relay-designs',
    validateSearch: parseRelayDesignSearch,
    beforeLoad: beforeLegacyDesignLoad,
  })
  const signIn = createRoute({
    getParentRoute: () => root,
    path: '/sign-in',
    component: () => <h1>Sign in destination</h1>,
  })
  const router = createRouter({
    routeTree: root.addChildren([page, legacyDesign, signIn]),
    history: createMemoryHistory({
      initialEntries: [initialEntry],
    }),
  })
  await router.load()
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  return router
}
function fillForm() {
  fireEvent.change(screen.getByLabelText('Username'), {
    target: { value: 'local_preview_user' },
  })
  fireEvent.change(screen.getByLabelText('Password'), {
    target: { value: 'Preview-Password-123' },
  })
  fireEvent.change(screen.getByLabelText('Confirm password'), {
    target: { value: 'Preview-Password-123' },
  })
}
beforeEach(() => {
  fixture.status = {
    register_enabled: true,
    password_register_enabled: true,
    email_verification: false,
    oauth_register_enabled: false,
    turnstile_check: false,
  }
  fixture.register.mockResolvedValue({ success: true })
  fixture.sendEmailVerification.mockResolvedValue({ success: true })
  vi.spyOn(window, 'scrollTo').mockImplementation(() => {})
  localStorage.clear()
  useAuthStore.getState().auth.reset()
})
afterEach(() => {
  cleanup()
  clients.splice(0).forEach((client) => client.clear())
  vi.clearAllMocks()
  useAuthStore.getState().auth.reset()
})

test('referral links remain intact while retired and invalid design parameters are ignored', () => {
  expect(
    parseRelayDesignSearch({ design: 'signal', aff: 'original-referral' })
  ).toEqual({ aff: 'original-referral' })
  expect(
    parseRelayDesignSearch({ design: '<script>', aff: 'original-referral' })
  ).toEqual({ aff: 'original-referral' })
  expect(parseRelayDesignSearch({ aff: 'original-referral' })).toEqual({
    aff: 'original-referral',
  })
  expect(parseRelayDesignSearch({ design: 'punch', aff: 123 })).toEqual({})
})

test('the retired gallery redirects to registration and preserves the original referral without registration requests', async () => {
  const router = await view(
    '/relay-designs?design=signal&aff=original-referral'
  )
  await screen.findByLabelText('Username')
  expect(router.state.location.pathname).toBe('/sign-up')
  expect(router.state.location.search).toEqual({ aff: 'original-referral' })
  expect(fixture.register).not.toHaveBeenCalled()
  expect(fixture.sendEmailVerification).not.toHaveBeenCalled()
})

test('registration has one Obsidian presentation and one form without design switching controls', async () => {
  await view('/sign-up?design=punch&aff=preview-referral')
  await screen.findByLabelText('Username')
  expect(
    screen.getByRole('heading', { name: 'One API. Your entire workflow.' })
  ).toBeVisible()
  expect(
    screen.queryByRole('navigation', { name: 'Compare registration designs' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Orange punch' })
  ).not.toBeInTheDocument()
  expect(document.querySelectorAll('form')).toHaveLength(1)
  expect(fixture.register).not.toHaveBeenCalled()
  expect(fixture.sendEmailVerification).not.toHaveBeenCalled()
})

test('pausing effects and opening the handbook preserve the same form and draft without sending requests', async () => {
  await view()
  const username = await screen.findByLabelText('Username')
  const password = screen.getByLabelText('Password')
  const form = username.closest('form')
  fillForm()
  fireEvent.click(screen.getByRole('button', { name: 'Pause effects' }))
  expect(
    screen.getByRole('button', { name: 'Enable effects' })
  ).toHaveAttribute('aria-pressed', 'false')
  fireEvent.click(screen.getByRole('button', { name: 'Connection handbook' }))
  expect(await screen.findByRole('dialog')).toBeVisible()
  await screen.findByRole('button', { name: 'Download Markdown' })
  fireEvent.click(screen.getByRole('button', { name: 'Close' }))
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  expect(
    screen.getByRole('button', { name: 'Enable effects' })
  ).toHaveAttribute('aria-pressed', 'false')
  expect(screen.getByLabelText('Username')).toBe(username)
  expect(screen.getByLabelText('Password')).toBe(password)
  expect(username.closest('form')).toBe(form)
  expect(username).toHaveValue('local_preview_user')
  expect(password).toHaveValue('Preview-Password-123')
  fireEvent.click(screen.getByRole('button', { name: 'Enable effects' }))
  expect(screen.getByRole('button', { name: 'Pause effects' })).toHaveAttribute(
    'aria-pressed',
    'true'
  )
  expect(fixture.register).not.toHaveBeenCalled()
  expect(fixture.sendEmailVerification).not.toHaveBeenCalled()
})

test.each([
  {
    register_enabled: false,
    password_register_enabled: true,
    message:
      'New account registration is currently closed. Existing accounts can still sign in.',
  },
  {
    register_enabled: true,
    password_register_enabled: false,
    message:
      'Password registration is currently closed. Use an available sign-in method.',
  },
  {
    register_enabled: undefined,
    password_register_enabled: true,
    message: 'Registration status unavailable',
  },
])(
  'closed or unknown registration does not present an active password form: %j',
  async ({ message, ...flags }) => {
    Object.assign(fixture.status, flags)
    await view()
    await screen.findByText(message)
    expect(screen.queryByLabelText('Username')).not.toBeInTheDocument()
    expect(fixture.register).not.toHaveBeenCalled()
  }
)

test('password mismatch and required email verification still block submission', async () => {
  fixture.status.email_verification = true
  await view()
  await screen.findByLabelText('Username')
  fillForm()
  expect(screen.getByLabelText('Verification code')).toHaveAttribute(
    'autoComplete',
    'one-time-code'
  )
  fireEvent.change(screen.getByLabelText('Confirm password'), {
    target: { value: 'Different-password' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Create account' }))
  await screen.findByText("Passwords don't match.")
  expect(fixture.register).not.toHaveBeenCalled()
  fireEvent.change(screen.getByLabelText('Confirm password'), {
    target: { value: 'Preview-Password-123' },
  })
  fireEvent.click(screen.getByRole('button', { name: 'Create account' }))
  expect(fixture.register).not.toHaveBeenCalled()
  expect(fixture.sendEmailVerification).not.toHaveBeenCalled()
})

test('registration sends one request and ends at sign-in without fabricating a session', async () => {
  const router = await view()
  await screen.findByLabelText('Username')
  fillForm()
  fireEvent.click(screen.getByRole('button', { name: 'Create account' }))
  await waitFor(() => expect(fixture.register).toHaveBeenCalledTimes(1))
  expect(fixture.register).toHaveBeenCalledWith(
    expect.objectContaining({
      username: 'local_preview_user',
      password: 'Preview-Password-123',
    })
  )
  await waitFor(() => expect(router.state.location.pathname).toBe('/sign-in'))
  expect(useAuthStore.getState().auth.user).toBeNull()
  expect(useAuthStore.getState().auth.accessToken).toBeNull()
})
