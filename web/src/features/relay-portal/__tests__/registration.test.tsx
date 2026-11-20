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
import { useAuthStore } from '@/stores/auth-store'

import { parseRelayDesignSearch } from '../search'
import { relayThemes } from '../themes'

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
const clients: QueryClient[] = []
async function view() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  clients.push(client)
  const root = createRootRoute()
  const page = createRoute({
    getParentRoute: () => root,
    path: '/relay-designs',
    validateSearch: parseRelayDesignSearch,
    component: () => <SignUp comparison />,
  })
  const signIn = createRoute({
    getParentRoute: () => root,
    path: '/sign-in',
    component: () => <h1>Sign in destination</h1>,
  })
  const router = createRouter({
    routeTree: root.addChildren([page, signIn]),
    history: createMemoryHistory({
      initialEntries: ['/relay-designs?design=signal&aff=preview-referral'],
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

test('all five theme values are bounded and referral links remain intact', () => {
  expect(relayThemes.map((theme) => theme.id)).toEqual([
    'signal',
    'punch',
    'circuit',
    'tide',
    'folio',
  ])
  expect(
    parseRelayDesignSearch({ design: '<script>', aff: 'original-referral' })
  ).toEqual({ design: 'signal', aff: 'original-referral' })
})

test('switching all five layouts preserves one form and its draft without registration or verification requests', async () => {
  const router = await view()
  await screen.findByLabelText('Username')
  fillForm()
  const username = screen.getByLabelText('Username')
  const password = screen.getByLabelText('Password')
  for (const theme of relayThemes) {
    fireEvent.click(screen.getByRole('button', { name: theme.nameKey }))
    await waitFor(() =>
      expect(router.state.location.search.design).toBe(theme.id)
    )
    expect(router.state.location.search.aff).toBe('preview-referral')
    expect(screen.getByLabelText('Username')).toBe(username)
    expect(screen.getByLabelText('Password')).toBe(password)
    expect(username).toHaveValue('local_preview_user')
    expect(password).toHaveValue('Preview-Password-123')
    expect(document.querySelectorAll('form')).toHaveLength(1)
  }
  expect(fixture.register).not.toHaveBeenCalled()
  expect(fixture.sendEmailVerification).not.toHaveBeenCalled()
})

test('the embedded handbook preserves the registration draft', async () => {
  await view()
  const username = await screen.findByLabelText('Username')
  fillForm()
  fireEvent.click(screen.getByRole('button', { name: 'Connection handbook' }))
  expect(await screen.findByRole('dialog')).toBeVisible()
  await screen.findByRole('button', { name: 'Download Markdown' })
  fireEvent.click(screen.getByRole('button', { name: 'Close' }))
  await waitFor(() =>
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  )
  expect(screen.getByLabelText('Username')).toBe(username)
  expect(username).toHaveValue('local_preview_user')
  expect(fixture.register).not.toHaveBeenCalled()
  expect(fixture.sendEmailVerification).not.toHaveBeenCalled()
})

test('effects can be paused across design changes without disturbing the registration draft or sending requests', async () => {
  await view()
  const username = await screen.findByLabelText('Username')
  fillForm()
  fireEvent.click(screen.getByRole('button', { name: 'Pause effects' }))
  expect(
    screen.getByRole('button', { name: 'Enable effects' })
  ).toHaveAttribute('aria-pressed', 'false')
  fireEvent.click(screen.getByRole('button', { name: 'Mint tide' }))
  await waitFor(() =>
    expect(screen.getByRole('button', { name: 'Mint tide' })).toHaveAttribute(
      'aria-pressed',
      'true'
    )
  )
  expect(screen.getByRole('button', { name: 'Enable effects' })).toBeVisible()
  expect(screen.getByLabelText('Username')).toBe(username)
  expect(username).toHaveValue('local_preview_user')
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
