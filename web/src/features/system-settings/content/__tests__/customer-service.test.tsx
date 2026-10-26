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
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import type { AxiosResponse } from 'axios'
import { useState, type ReactNode } from 'react'
import { afterEach, beforeEach, expect, test, vi } from 'vitest'

import { AssetUpload } from '@/features/creation-content/components/asset-upload'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { SettingsPageProvider } from '../../components/settings-page-context'
import {
  loadCustomerService,
  saveCustomerService,
} from '../customer-service-api'
import { CustomerServiceSection } from '../customer-service-section'
import {
  CUSTOMER_SERVICE_QUERY_KEY,
  customerServiceAssetId,
  customerServiceAssetPath,
  customerServiceSchema,
  EMPTY_CUSTOMER_SERVICE,
  type CustomerService,
} from '../lib/customer-service'

const assetId = `cc_${'a'.repeat(64)}`
const assetPath = `/api/creation/assets/${assetId}`
let saved: CustomerService
const baseURL = api.defaults.baseURL
const adapter = api.defaults.adapter

function renderSettings(content: ReactNode = <CustomerServiceSection />) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  function Harness() {
    const [actionsContainer, setActionsContainer] =
      useState<HTMLDivElement | null>(null)
    return (
      <QueryClientProvider client={client}>
        <div ref={setActionsContainer} />
        <SettingsPageProvider actionsContainer={actionsContainer}>
          {content}
        </SettingsPageProvider>
      </QueryClientProvider>
    )
  }
  return { client, ...render(<Harness />) }
}

async function openDisplaySettings() {
  const button = await screen.findByRole('button', {
    name: 'Optional display settings',
  })
  if (button.getAttribute('aria-expanded') !== 'true') fireEvent.click(button)
}
async function findOptionalTextbox(options: { name: string }) {
  await openDisplaySettings()
  return screen.findByRole('textbox', options)
}
async function findOptionalSwitch(options: { name: string }) {
  await openDisplaySettings()
  return screen.findByRole('switch', options)
}

beforeEach(() => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'root', role: ROLE.SUPER_ADMIN })
  saved = { ...EMPTY_CUSTOMER_SERVICE }
  vi.spyOn(api, 'get').mockImplementation(async () => ({
    data: { success: true, data: { ...saved } },
  }))
  vi.spyOn(api, 'put').mockImplementation(async (_url, data) => {
    saved = data as CustomerService
    return { data: { success: true, data: { ...saved } } }
  })
  vi.spyOn(api, 'post').mockResolvedValue({
    data: {
      success: true,
      data: { id: assetId, mediaType: 'image/png', size: 12, url: assetPath },
    },
  })
})

afterEach(() => {
  api.defaults.baseURL = baseURL
  api.defaults.adapter = adapter
})

test('a loading request cannot save a blank configuration', () => {
  vi.mocked(api.get).mockReturnValue(new Promise(() => undefined))
  renderSettings()
  expect(screen.getByRole('status')).toHaveTextContent(
    'Loading customer service settings...'
  )
  expect(
    screen.queryByRole('button', { name: 'Save customer service settings' })
  ).not.toBeInTheDocument()
})

test('a failed initial load offers retry without an editable empty fallback', async () => {
  vi.mocked(api.get).mockRejectedValueOnce(new Error('offline'))
  const user = userEvent.setup()
  renderSettings()
  expect(
    await screen.findByText('Unable to load customer service settings')
  ).toBeVisible()
  expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
  await user.click(screen.getByRole('button', { name: 'Retry' }))
  expect(
    await findOptionalTextbox({
      name: 'Customer service title (optional)',
    })
  ).toHaveValue('')
})

test('an empty disabled configuration is explicit and unchanged settings cannot save', async () => {
  renderSettings()
  expect(
    await findOptionalSwitch({
      name: 'Enable customer service QR code',
    })
  ).not.toBeChecked()
  expect(
    screen.getByText(
      'No customer service QR code configured. Users will see an unconfigured message.'
    )
  ).toBeVisible()
  expect(
    screen.getByRole('button', { name: 'Save customer service settings' })
  ).toBeDisabled()
  expect(api.put).not.toHaveBeenCalled()
})

test('keyboard enabling without an image shows validation and does not send a save request', async () => {
  const user = userEvent.setup()
  renderSettings()
  const toggle = await findOptionalSwitch({
    name: 'Enable customer service QR code',
  })
  toggle.focus()
  await user.keyboard(' ')
  expect(toggle).toBeChecked()
  await user.click(
    screen.getByRole('button', { name: 'Save customer service settings' })
  )
  expect(
    await screen.findByText(
      'Upload a customer service QR code before enabling it'
    )
  ).toBeVisible()
  expect(
    screen.getByLabelText('Customer service QR code', { selector: 'input' })
  ).toHaveAttribute('aria-invalid', 'true')
  expect(api.put).not.toHaveBeenCalled()
})

test.each(['', '/harness'])(
  'uploading an image at base path %s previews it and saves a canonical path atomically',
  async (basePath) => {
    api.defaults.baseURL = basePath
    const user = userEvent.setup()
    const view = renderSettings()
    const invalidation = vi.spyOn(view.client, 'invalidateQueries')
    await user.upload(
      await screen.findByLabelText('Customer service QR code'),
      new File(['png'], 'support.png', { type: 'image/png' })
    )
    expect(
      await screen.findByRole('img', { name: 'Customer service QR code' })
    ).toHaveAttribute('src', `${basePath}${assetPath}`)
    await openDisplaySettings()
    await user.clear(
      screen.getByRole('textbox', { name: 'Customer service title (optional)' })
    )
    await user.clear(
      screen.getByRole('textbox', {
        name: 'Customer service description (optional)',
      })
    )
    await user.type(
      screen.getByRole('textbox', {
        name: 'Customer service title (optional)',
      }),
      'Support'
    )
    await user.type(
      screen.getByRole('textbox', {
        name: 'Customer service description (optional)',
      }),
      'Scan to contact us'
    )
    expect(
      screen.getByRole('switch', { name: 'Enable customer service QR code' })
    ).toBeChecked()
    await user.click(
      screen.getByRole('button', { name: 'Save customer service settings' })
    )
    await waitFor(() =>
      expect(api.put).toHaveBeenCalledWith('/api/customer-service', {
        schema_version: 1,
        enabled: true,
        qr_code_url: assetPath,
        title: 'Support',
        description: 'Scan to contact us',
      })
    )
    expect(api.post).toHaveBeenCalledWith(
      '/api/customer-service/assets',
      expect.any(FormData)
    )
    expect(invalidation).toHaveBeenCalledWith({ queryKey: ['status'] })
    expect(invalidation).toHaveBeenCalledWith({
      queryKey: CUSTOMER_SERVICE_QUERY_KEY,
    })
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Save customer service settings' })
      ).toBeDisabled()
    )
  }
)

test('a failed save retains the draft through a background refetch and requires an explicit retry', async () => {
  const user = userEvent.setup()
  vi.mocked(api.put).mockResolvedValue({
    data: { success: false, message: 'Save rejected' },
  })
  const view = renderSettings()
  const title = await findOptionalTextbox({
    name: 'Customer service title (optional)',
  })
  await user.type(title, 'Local draft')
  await user.click(
    screen.getByRole('button', { name: 'Save customer service settings' })
  )
  expect(
    await screen.findByText('Unable to save customer service settings')
  ).toBeVisible()
  expect(screen.getByText('Save rejected')).toBeVisible()
  act(() => {
    view.client.setQueryData(CUSTOMER_SERVICE_QUERY_KEY, {
      ...saved,
      title: 'Server title',
    })
  })
  expect(title).toHaveValue('Local draft')
  expect(api.put).toHaveBeenCalledTimes(1)
  expect(
    screen.getByRole('button', { name: 'Save customer service settings' })
  ).toBeEnabled()
})

test('a pending save disables editing, removal and repeated submission', async () => {
  const user = userEvent.setup()
  saved = { ...saved, enabled: true, qr_code_url: assetPath }
  let finish:
    | ((value: { data: { success: boolean; data: CustomerService } }) => void)
    | undefined
  vi.mocked(api.put).mockReturnValue(
    new Promise((resolve) => {
      finish = resolve
    })
  )
  renderSettings()
  const title = await findOptionalTextbox({
    name: 'Customer service title (optional)',
  })
  await user.type(title, 'Pending')
  await user.click(
    screen.getByRole('button', { name: 'Save customer service settings' })
  )
  expect(
    await screen.findByRole('button', { name: 'Saving...' })
  ).toBeDisabled()
  expect(title).toBeDisabled()
  expect(screen.getByRole('button', { name: 'Remove asset' })).toBeDisabled()
  fireEvent.submit(
    screen.getByRole('form', { name: 'Customer service settings' })
  )
  await waitFor(() => expect(api.put).toHaveBeenCalledTimes(1))
  await act(async () => {
    finish?.({ data: { success: true, data: { ...saved, title: 'Pending' } } })
  })
})

test('removing an enabled QR code requires disabling before saving the empty draft', async () => {
  const user = userEvent.setup()
  saved = {
    ...saved,
    enabled: true,
    qr_code_url: assetPath,
    title: 'Retained draft',
  }
  renderSettings()
  await user.click(await screen.findByRole('button', { name: 'Remove asset' }))
  expect(screen.queryByRole('img')).not.toBeInTheDocument()
  await user.click(
    screen.getByRole('button', { name: 'Save customer service settings' })
  )
  expect(
    await screen.findByText(
      'Upload a customer service QR code before enabling it'
    )
  ).toBeVisible()
  await openDisplaySettings()
  await user.click(
    screen.getByRole('switch', { name: 'Enable customer service QR code' })
  )
  await user.click(
    screen.getByRole('button', { name: 'Save customer service settings' })
  )
  await waitFor(() =>
    expect(api.put).toHaveBeenCalledWith('/api/customer-service', {
      ...saved,
      enabled: false,
      qr_code_url: '',
    })
  )
})

test('previews follow the API deployment prefix and render stored titles as plain text', async () => {
  api.defaults.baseURL = 'https://api.example/harness/'
  saved = {
    ...saved,
    qr_code_url: assetPath,
    title: '<img src=x onerror=alert(1)>',
  }
  renderSettings()
  expect(
    await screen.findByRole('img', { name: 'Customer service QR code' })
  ).toHaveAttribute('src', `https://api.example/harness${assetPath}`)
  await openDisplaySettings()
  expect(
    screen.getByRole('textbox', { name: 'Customer service title (optional)' })
  ).toHaveValue(saved.title)
  expect(screen.getAllByRole('img')).toHaveLength(1)
})

test('the shared uploader keeps existing cover and video labels when no override is supplied', () => {
  renderSettings(
    <>
      <AssetUpload
        kind='image'
        value={assetId}
        onChange={vi.fn()}
        onBusyChange={vi.fn()}
      />
      <AssetUpload
        kind='video'
        value={assetId}
        onChange={vi.fn()}
        onBusyChange={vi.fn()}
      />
    </>
  )
  expect(
    screen.getByLabelText('Template cover', { selector: 'input' })
  ).toHaveAttribute('accept', 'image/png,image/jpeg,image/webp')
  expect(
    screen.getByLabelText('Demo video (optional)', { selector: 'input' })
  ).toHaveAttribute('accept', 'video/mp4,video/webm')
  expect(screen.getByRole('img', { name: 'Template cover' })).toHaveAttribute(
    'src',
    assetPath
  )
})

test('an oversized image fails upload without replacing the saved QR code', async () => {
  const user = userEvent.setup()
  saved = { ...saved, qr_code_url: assetPath }
  renderSettings()
  const file = new File(['small'], 'large.png', { type: 'image/png' })
  Object.defineProperty(file, 'size', { value: 10 * 1024 * 1024 + 1 })
  await user.upload(
    await screen.findByLabelText('Customer service QR code', {
      selector: 'input',
    }),
    file
  )
  await waitFor(() =>
    expect(
      screen.getByLabelText('Customer service QR code', { selector: 'input' })
    ).toBeEnabled()
  )
  expect(api.post).not.toHaveBeenCalled()
  expect(
    screen.getByRole('img', { name: 'Customer service QR code' })
  ).toHaveAttribute('src', assetPath)
})

test('the title and description limits count UTF-16 units', () => {
  expect(
    customerServiceSchema.safeParse({
      ...EMPTY_CUSTOMER_SERVICE,
      title: '😀'.repeat(32),
      description: '😀'.repeat(128),
    }).success
  ).toBe(true)
  expect(
    customerServiceSchema.safeParse({
      ...EMPTY_CUSTOMER_SERVICE,
      title: '😀'.repeat(33),
    }).success
  ).toBe(false)
  expect(
    customerServiceSchema.safeParse({
      ...EMPTY_CUSTOMER_SERVICE,
      description: '😀'.repeat(129),
    }).success
  ).toBe(false)
})

test.each([
  'https://external.example/code.png',
  'data:image/png;base64,x',
  '/api/creation/assets/cc_BAD',
  '//evil.example/x',
  `${assetPath}?x=1`,
  `/harness${assetPath}`,
  `${assetPath}/extra`,
])(
  'unsupported asset URL %s is rejected before saving',
  async (qr_code_url) => {
    await expect(
      saveCustomerService({ ...EMPTY_CUSTOMER_SERVICE, qr_code_url })
    ).rejects.toThrow()
    expect(api.put).not.toHaveBeenCalled()
  }
)

test('the canonical customer service asset path round-trips its exact ID and clears on removal', () => {
  expect(customerServiceAssetPath(assetId)).toBe(assetPath)
  expect(customerServiceAssetId(assetPath)).toBe(assetId)
  expect(customerServiceAssetPath(null)).toBe('')
  expect(customerServiceAssetId('')).toBeNull()
})

test.each([
  `/harness${assetPath}`,
  `https://external.example${assetPath}`,
  `${assetPath}?x=1`,
  '/api/creation/assets/cc_BAD',
])('the QR preview rejects noncanonical asset path %s', (value) => {
  expect(customerServiceAssetId(value)).toBeNull()
})

test('control characters, unsupported schemas and unknown fields fail closed', () => {
  expect(
    customerServiceSchema.safeParse({
      ...EMPTY_CUSTOMER_SERVICE,
      title: 'a\nb',
    }).success
  ).toBe(false)
  expect(
    customerServiceSchema.safeParse({
      ...EMPTY_CUSTOMER_SERVICE,
      description: 'a\u0085b',
    }).success
  ).toBe(false)
  expect(
    customerServiceSchema.safeParse({
      ...EMPTY_CUSTOMER_SERVICE,
      schema_version: 2,
    }).success
  ).toBe(false)
  expect(
    customerServiceSchema.safeParse({
      ...EMPTY_CUSTOMER_SERVICE,
      wechat_qrcode: assetPath,
    }).success
  ).toBe(false)
})

test('malformed stored configuration fails loading rather than becoming an editable default', async () => {
  vi.mocked(api.get).mockResolvedValue({
    data: {
      success: true,
      data: { ...saved, qr_code_url: 'https://external.example/image.png' },
    },
  })
  await expect(loadCustomerService()).rejects.toThrow()
})

test('a late older GET cannot overwrite the successfully saved form or cache', async () => {
  vi.mocked(api.get).mockRestore()
  vi.mocked(api.put).mockRestore()
  let gets = 0
  let finishOld: (() => void) | undefined
  api.defaults.adapter = async (config) => {
    const response = (data: CustomerService): AxiosResponse => ({
      data: { success: true, data },
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    })
    if (config.method === 'put') {
      saved = JSON.parse(String(config.data)) as CustomerService
      return response({ ...saved })
    }
    gets++
    if (gets === 2) {
      const stale = { ...saved, title: 'Old server title' }
      return new Promise<AxiosResponse>((resolve) => {
        finishOld = () => resolve(response(stale))
      })
    }
    return response({ ...saved })
  }
  const user = userEvent.setup()
  const view = renderSettings()
  const title = await findOptionalTextbox({
    name: 'Customer service title (optional)',
  })
  act(() => {
    void view.client.invalidateQueries({ queryKey: CUSTOMER_SERVICE_QUERY_KEY })
  })
  await waitFor(() => expect(finishOld).toBeTypeOf('function'))
  await user.type(title, 'New saved title')
  await user.click(
    screen.getByRole('button', { name: 'Save customer service settings' })
  )
  await waitFor(() => expect(saved.title).toBe('New saved title'))
  await waitFor(() => expect(gets).toBe(3))
  await act(async () => {
    finishOld?.()
  })
  expect(title).toHaveValue('New saved title')
  expect(view.client.getQueryData(CUSTOMER_SERVICE_QUERY_KEY)).toEqual(saved)
})

test('an uploading QR code disables saving until the upload finishes', async () => {
  const user = userEvent.setup()
  let finish: (() => void) | undefined
  vi.mocked(api.post).mockImplementation(
    () =>
      new Promise((resolve) => {
        finish = () =>
          resolve({
            data: {
              success: true,
              data: {
                id: assetId,
                mediaType: 'image/png',
                size: 12,
                url: assetPath,
              },
            },
          })
      })
  )
  renderSettings()
  await user.type(
    await findOptionalTextbox({
      name: 'Customer service title (optional)',
    }),
    'Draft'
  )
  await user.upload(
    screen.getByLabelText('Customer service QR code', { selector: 'input' }),
    new File(['image'], 'qr.png', { type: 'image/png' })
  )
  expect(await screen.findByRole('status')).toHaveTextContent('Uploading...')
  expect(
    screen.getByRole('button', { name: 'Save customer service settings' })
  ).toBeDisabled()
  expect(
    screen.getByRole('textbox', { name: 'Customer service title (optional)' })
  ).toBeDisabled()
  await act(async () => {
    finish?.()
  })
  expect(
    await screen.findByRole('img', { name: 'Customer service QR code' })
  ).toHaveAttribute('src', assetPath)
  expect(
    screen.getByRole('button', { name: 'Save customer service settings' })
  ).toBeEnabled()
  expect(api.put).not.toHaveBeenCalled()
})

test('a description with a line break shows an accessible error without discarding the draft', async () => {
  const user = userEvent.setup()
  renderSettings()
  const description = await findOptionalTextbox({
    name: 'Customer service description (optional)',
  })
  fireEvent.change(description, { target: { value: 'First\nsecond' } })
  await user.click(
    screen.getByRole('button', { name: 'Save customer service settings' })
  )
  expect(await screen.findByRole('alert')).toHaveTextContent(
    'Customer service text must not contain control characters'
  )
  expect(description).toHaveAttribute('aria-invalid', 'true')
  expect(description).toHaveValue('First\nsecond')
  expect(api.put).not.toHaveBeenCalled()
})

test('read-only customer service permission shows the saved preview without upload, edit or save controls', async () => {
  saved = {
    ...saved,
    enabled: false,
    qr_code_url: assetPath,
    title: 'Saved support',
  }
  useAuthStore.getState().auth.setUser({
    id: 2,
    username: 'reader',
    role: ROLE.ADMIN,
    permissions: {
      admin_permissions: { customer_service: { read: true, write: false } },
    },
  })
  renderSettings()
  expect(
    await screen.findByRole('img', { name: 'Customer service QR code' })
  ).toBeVisible()
  expect(
    screen.queryByLabelText('Customer service QR code', { selector: 'input' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Remove asset' })
  ).not.toBeInTheDocument()
  expect(
    screen.queryByRole('button', { name: 'Save customer service settings' })
  ).not.toBeInTheDocument()
  expect(screen.queryByRole('textbox')).not.toBeInTheDocument()
  expect(api.post).not.toHaveBeenCalled()
  expect(api.put).not.toHaveBeenCalled()
})

test('a customer service denial performs no request or preview disclosure', () => {
  useAuthStore.getState().auth.setUser({
    id: 2,
    username: 'denied',
    role: ROLE.ADMIN,
    permissions: { admin_permissions: {} },
  })
  renderSettings()
  expect(screen.getByText('Access denied')).toBeVisible()
  expect(api.get).not.toHaveBeenCalled()
  expect(screen.queryByRole('img')).not.toBeInTheDocument()
})

test('the first successful upload prepares Chinese defaults and enabled draft but does not publish before saving', async () => {
  const user = userEvent.setup()
  renderSettings()
  await user.upload(
    await screen.findByLabelText('Customer service QR code', {
      selector: 'input',
    }),
    new File(['png'], 'support.png', { type: 'image/png' })
  )
  expect(await screen.findByRole('img')).toBeVisible()
  expect(api.post).toHaveBeenCalledWith(
    '/api/customer-service/assets',
    expect.any(FormData)
  )
  expect(api.put).not.toHaveBeenCalled()
  await user.click(
    screen.getByRole('button', { name: 'Save customer service settings' })
  )
  await waitFor(() =>
    expect(api.put).toHaveBeenCalledWith('/api/customer-service', {
      schema_version: 1,
      enabled: true,
      qr_code_url: assetPath,
      title: '联系客服',
      description: '请扫描二维码联系客服',
    })
  )
})

test('a previously disabled saved QR remains disabled after loading and replacing its image', async () => {
  const user = userEvent.setup()
  saved = { ...saved, enabled: false, qr_code_url: assetPath }
  renderSettings()
  await user.upload(
    await screen.findByLabelText('Customer service QR code', {
      selector: 'input',
    }),
    new File(['png'], 'replacement.png', { type: 'image/png' })
  )
  await openDisplaySettings()
  expect(
    screen.getByRole('switch', { name: 'Enable customer service QR code' })
  ).not.toBeChecked()
  expect(api.put).not.toHaveBeenCalled()
})
