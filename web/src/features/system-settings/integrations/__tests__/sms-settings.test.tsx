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
  createRouter,
  RouterProvider,
} from '@tanstack/react-router'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { useState } from 'react'
import { beforeEach, expect, test, vi } from 'vitest'

import { api } from '@/lib/api'

import { SettingsPageProvider } from '../../components/settings-page-context'
import { SmsSettingsSection } from '../sms-settings-section'

function Fixture() {
  const [container, setContainer] = useState<HTMLDivElement | null>(null)
  return (
    <>
      <div ref={setContainer} />
      <SettingsPageProvider actionsContainer={container}>
        <SmsSettingsSection
          defaultValues={{
            SmsAliyunAccessKeyId: 'test-access-key-id',
            // The options endpoint never returns the secret, so it always
            // arrives blank.
            SmsAliyunAccessKeySecret: '',
            SmsAliyunSignName: 'test-sign',
            SmsAliyunTemplateCode: 'SMS_0000001',
          }}
        />
      </SettingsPageProvider>
    </>
  )
}

async function renderSettings() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const router = createRouter({
    routeTree: createRootRoute({ component: Fixture }),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
  render(
    <QueryClientProvider client={client}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  )
  return screen.findByLabelText('AccessKey Secret')
}

beforeEach(() => {
  vi.spyOn(api, 'put').mockResolvedValue({ data: { success: true } })
})

test('a typed secret is submitted so the credential can be set', async () => {
  const user = userEvent.setup()
  const secret = await renderSettings()
  await user.type(secret, 'new-access-key-secret')
  await user.click(screen.getByRole('button', { name: 'Save SMS settings' }))

  await waitFor(() =>
    expect(api.put).toHaveBeenCalledWith('/api/option/', {
      key: 'SmsAliyunAccessKeySecret',
      value: 'new-access-key-secret',
    })
  )
})

test('a blank secret does not overwrite the stored one', async () => {
  const user = userEvent.setup()
  await renderSettings()
  const signName = await screen.findByLabelText('SMS sign name')
  await user.clear(signName)
  await user.type(signName, 'changed-sign')
  await user.click(screen.getByRole('button', { name: 'Save SMS settings' }))

  await waitFor(() =>
    expect(api.put).toHaveBeenCalledWith('/api/option/', {
      key: 'SmsAliyunSignName',
      value: 'changed-sign',
    })
  )
  expect(api.put).not.toHaveBeenCalledWith('/api/option/', {
    key: 'SmsAliyunAccessKeySecret',
    value: '',
  })
})

test('an unchanged form submits nothing', async () => {
  const user = userEvent.setup()
  await renderSettings()
  await user.click(screen.getByRole('button', { name: 'Save SMS settings' }))

  await waitFor(() => expect(api.put).not.toHaveBeenCalled())
})
