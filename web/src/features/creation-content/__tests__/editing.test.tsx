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
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest'

import { CreationContentPage } from '..'
import type { CreationContent, Template } from '../lib/schema'

const { get, put, post } = vi.hoisted(() => ({
  get: vi.fn(),
  put: vi.fn(),
  post: vi.fn(),
}))
vi.mock('@/lib/api', () => ({ api: { get, put, post } }))
const clients: QueryClient[] = []
function content(): CreationContent {
  const mode = () => ({
    title: { zh: '' },
    description: { zh: '' },
    promptPlaceholder: { zh: '' },
    categories: [],
    templates: [],
    presets: [],
  })
  return { schemaVersion: 1, revision: '0', image: mode(), video: mode() }
}
function renderPage() {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  clients.push(client)
  render(
    <QueryClientProvider client={client}>
      <CreationContentPage />
    </QueryClientProvider>
  )
}
const coverAssetId = `cc_${'a'.repeat(64)}`
function existingTemplate(): Template {
  return {
    id: 'existing',
    categoryId: null,
    title: { zh: 'Existing template' },
    description: { zh: 'Keep this description' },
    prompt: { zh: 'Keep this prompt' },
    coverAssetId,
    demoVideoAssetId: null,
    sortOrder: 0,
    status: 'published',
  }
}
async function openFilledTemplate(
  user: ReturnType<typeof userEvent.setup>,
  video = false
): Promise<HTMLElement> {
  await screen.findByRole('button', { name: 'Add template' })
  if (video) await user.click(screen.getByRole('tab', { name: 'Videos' }))
  await user.click(screen.getByRole('button', { name: 'Add template' }))
  const dialog = await screen.findByRole('dialog')
  await user.type(
    within(dialog).getByLabelText('Title · Chinese'),
    'My new template'
  )
  await user.type(
    within(dialog).getByLabelText('Description · Chinese'),
    'Clean composition'
  )
  await user.type(
    within(dialog).getByLabelText('Prompt · Chinese'),
    'A red cup under soft light'
  )
  return dialog
}
beforeEach(() => {
  get.mockResolvedValue({ data: { success: true, data: content() } })
  put.mockImplementation((_url: string, body: CreationContent) =>
    Promise.resolve({
      data: { success: true, data: { ...body, revision: 'next' } },
    })
  )
})
afterEach(() => {
  cleanup()
  clients.forEach((client) => client.clear())
  clients.length = 0
  vi.resetAllMocks()
})
describe('creation content editing', () => {
  test('a long management page has a keyboard-focusable scroll area while its title and save actions stay outside', async () => {
    renderPage()
    await screen.findByRole('button', { name: 'Add template' })
    const scrollArea = screen.getByRole('region', {
      name: 'AI Creation Management',
    })
    expect(screen.getByRole('main')).toHaveClass(
      'min-h-0',
      'flex-1',
      'overflow-hidden'
    )
    expect(scrollArea.parentElement).toHaveClass(
      'min-h-0',
      'flex-1',
      'overflow-hidden'
    )
    expect(scrollArea).toHaveClass('h-full', 'min-h-0', 'overflow-y-auto')
    expect(scrollArea).toHaveAttribute('tabindex', '0')
    scrollArea.focus()
    expect(scrollArea).toHaveFocus()
    expect(scrollArea).not.toContainElement(
      screen.getByRole('heading', { name: 'AI Creation Management' })
    )
    expect(scrollArea).not.toContainElement(
      screen.getByRole('button', { name: 'Save content' })
    )
  })
  test('template records appear before page copy and category editing', async () => {
    renderPage()
    await screen.findByRole('button', { name: 'Add template' })
    const templates = screen.getByText('Creation templates')
    for (const section of ['Creation page copy', 'Creation categories']) {
      expect(
        templates.compareDocumentPosition(screen.getByText(section)) &
          Node.DOCUMENT_POSITION_FOLLOWING
      ).toBeTruthy()
    }
  })
  test.each(['image', 'video'] as const)(
    'loading saved %s templates shows their existing covers and opens their saved prompt for editing',
    async (mode) => {
      const user = userEvent.setup()
      const initial = content()
      const template = {
        ...existingTemplate(),
        title: { zh: `Saved ${mode} template` },
        prompt: { zh: `Saved ${mode} prompt` },
        demoVideoAssetId: mode === 'video' ? `cc_${'b'.repeat(64)}` : null,
      }
      initial[mode].templates = [template]
      get.mockResolvedValue({ data: { success: true, data: initial } })
      renderPage()
      await screen.findByRole('button', { name: 'Add template' })
      if (mode === 'video') {
        await user.click(screen.getByRole('tab', { name: 'Videos' }))
      }
      expect(screen.getByAltText(template.title.zh)).toHaveAttribute(
        'src',
        `/api/creation/assets/${coverAssetId}`
      )
      await user.click(
        screen.getByRole('button', {
          name: `Edit template: ${template.title.zh}`,
        })
      )
      const dialog = await screen.findByRole('dialog')
      expect(within(dialog).getByLabelText('Title · Chinese')).toHaveValue(
        template.title.zh
      )
      expect(within(dialog).getByLabelText('Prompt · Chinese')).toHaveValue(
        template.prompt.zh
      )
      expect(within(dialog).getByAltText('Template cover')).toHaveAttribute(
        'src',
        `/api/creation/assets/${coverAssetId}`
      )
      if (mode === 'video') {
        expect(
          within(dialog).getByLabelText('Demo video (optional)', {
            selector: 'video',
          })
        ).toHaveAttribute(
          'src',
          `/api/creation/assets/${template.demoVideoAssetId}`
        )
      }
      expect(put).not.toHaveBeenCalled()
    }
  )
  test('empty content shows a real empty state and keyboard switches image and video modes', async () => {
    const user = userEvent.setup()
    renderPage()
    expect(await screen.findByText('No creation templates yet')).toBeVisible()
    const image = screen.getByRole('tab', { name: 'Images' })
    const video = screen.getByRole('tab', { name: 'Videos' })
    expect(image).toHaveAttribute('aria-selected', 'true')
    image.focus()
    await user.keyboard('{ArrowRight}')
    expect(video).toHaveFocus()
    await user.keyboard('{Enter}')
    expect(video).toHaveAttribute('aria-selected', 'true')
    expect(screen.getByText('Camera and style presets')).toBeVisible()
    expect(screen.getByRole('button', { name: 'Save content' })).toBeDisabled()
  })
  test('editing Chinese page copy saves the complete versioned document and clears dirty state', async () => {
    const user = userEvent.setup()
    renderPage()
    const title = await screen.findByLabelText('Page title · Chinese')
    await user.type(title, '我的创作')
    await user.click(screen.getByRole('button', { name: 'Save content' }))
    await waitFor(() => expect(put).toHaveBeenCalledOnce())
    expect(put.mock.calls[0][1]).toMatchObject({
      schemaVersion: 1,
      revision: '0',
      image: { title: { zh: '我的创作' } },
      video: { templates: [] },
    })
    await waitFor(() =>
      expect(
        screen.getByRole('button', { name: 'Save content' })
      ).toBeDisabled()
    )
  })
  test('a version conflict retains edits and only explicit confirmed reload replaces them', async () => {
    const user = userEvent.setup()
    put.mockRejectedValue({
      response: {
        status: 409,
        data: {
          success: false,
          message: 'Creation content changed; reload before saving',
        },
      },
    })
    renderPage()
    const title = await screen.findByLabelText('Page title · Chinese')
    await user.type(title, '保留草稿')
    await user.click(screen.getByRole('button', { name: 'Save content' }))
    expect(
      await screen.findByText(
        'Your edits are retained. Copy any changes you want to keep before reloading the latest version.'
      )
    ).toBeVisible()
    expect(title).toHaveValue('保留草稿')
    await user.click(screen.getByRole('button', { name: 'Reload content' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(title).toHaveValue('保留草稿')
    await user.click(
      within(dialog).getByRole('button', { name: 'Reload content' })
    )
    await waitFor(() => expect(title).toHaveValue(''))
  })
  test('a cover upload publishes the image template directly from the dialog', async () => {
    const user = userEvent.setup()
    const asset = `cc_${'a'.repeat(64)}`
    post.mockResolvedValue({
      data: {
        success: true,
        data: {
          id: asset,
          mediaType: 'image/png',
          size: 8,
          url: `/api/creation/assets/${asset}`,
        },
      },
    })
    renderPage()
    await user.click(
      await screen.findByRole('button', { name: 'Add template' })
    )
    const dialog = await screen.findByRole('dialog')
    await user.type(
      within(dialog).getByLabelText('Title · Chinese'),
      '商品摄影'
    )
    await user.type(
      within(dialog).getByLabelText('Description · Chinese'),
      '干净自然'
    )
    await user.type(
      within(dialog).getByLabelText('Prompt · Chinese'),
      '用柔和自然光拍摄商品'
    )
    await user.upload(
      within(dialog).getByLabelText('Template cover'),
      new File(['test-image'], 'cover.png', { type: 'image/png' })
    )
    await waitFor(() =>
      expect(within(dialog).getByAltText('Template cover')).toHaveAttribute(
        'src',
        `/api/creation/assets/${asset}`
      )
    )
    expect(post.mock.calls[0][1]).toBeInstanceOf(FormData)
    await user.click(within(dialog).getByRole('button', { name: 'Publish' }))
    await waitFor(() => expect(put).toHaveBeenCalledOnce())
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
    expect(put.mock.calls[0][1].image.templates[0]).toMatchObject({
      title: { zh: '商品摄影' },
      prompt: { zh: '用柔和自然光拍摄商品' },
      coverAssetId: asset,
      status: 'published',
    })
  })
  test('failed upload keeps the edited prompt and does not mark a template as published', async () => {
    const user = userEvent.setup()
    post.mockRejectedValue(new Error('Upload failed'))
    renderPage()
    await user.click(
      await screen.findByRole('button', { name: 'Add template' })
    )
    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByLabelText('Title · Chinese'), '测试')
    await user.type(
      within(dialog).getByLabelText('Description · Chinese'),
      '描述'
    )
    const prompt = within(dialog).getByLabelText('Prompt · Chinese')
    await user.type(prompt, '保留这段提示词')
    await user.upload(
      within(dialog).getByLabelText('Template cover'),
      new File(['test'], 'cover.png', { type: 'image/png' })
    )
    await waitFor(() =>
      expect(within(dialog).getByLabelText('Template cover')).toBeEnabled()
    )
    expect(prompt).toHaveValue('保留这段提示词')
    await user.click(within(dialog).getByRole('button', { name: 'Publish' }))
    expect(
      await within(dialog).findByText('Published templates require a cover')
    ).toBeVisible()
    expect(put).not.toHaveBeenCalled()
  })
  test('a new empty preset is blocked from save and required fields are marked invalid', async () => {
    const user = userEvent.setup()
    renderPage()
    await user.click(await screen.findByRole('button', { name: 'Add preset' }))
    await user.click(screen.getByRole('button', { name: 'Save content' }))
    expect(await screen.findByText('Check creation content')).toBeVisible()
    expect(screen.getByLabelText('Preset name · Chinese')).toHaveAttribute(
      'aria-invalid',
      'true'
    )
    expect(put).not.toHaveBeenCalled()
  })
  test('video publish saves its cover and demo asset with both modes of valid unsaved copy', async () => {
    const user = userEvent.setup()
    const demoAssetId = `cc_${'b'.repeat(64)}`
    post.mockImplementation((_url: string, data: FormData) => {
      const file = data.get('file') as File
      const video = file.type === 'video/mp4'
      return Promise.resolve({
        data: {
          success: true,
          data: {
            id: video ? demoAssetId : coverAssetId,
            mediaType: file.type,
            size: file.size,
            url: `/api/creation/assets/${video ? demoAssetId : coverAssetId}`,
          },
        },
      })
    })
    renderPage()
    await user.type(
      await screen.findByLabelText('Page title · Chinese'),
      'Unsaved image title'
    )
    await user.click(screen.getByRole('tab', { name: 'Videos' }))
    await user.type(
      screen.getByLabelText('Page title · Chinese'),
      'Unsaved video title'
    )
    const dialog = await openFilledTemplate(user)
    await user.upload(
      within(dialog).getByLabelText('Template cover'),
      new File(['cover'], 'cover.png', { type: 'image/png' })
    )
    await waitFor(() =>
      expect(within(dialog).getByAltText('Template cover')).toBeVisible()
    )
    await user.upload(
      within(dialog).getByLabelText('Demo video (optional)'),
      new File(['video'], 'demo.mp4', { type: 'video/mp4' })
    )
    await waitFor(() =>
      expect(
        within(dialog).getByLabelText('Demo video (optional)', {
          selector: 'video',
        })
      ).toHaveAttribute('src', `/api/creation/assets/${demoAssetId}`)
    )
    expect(
      within(dialog).queryByLabelText('Publication status')
    ).not.toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Publish' }))
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
    expect(put).toHaveBeenCalledOnce()
    expect(put.mock.calls[0][1]).toMatchObject({
      revision: '0',
      image: { title: { zh: 'Unsaved image title' }, templates: [] },
      video: {
        title: { zh: 'Unsaved video title' },
        templates: [
          { coverAssetId, demoVideoAssetId: demoAssetId, status: 'published' },
        ],
      },
    })
  })
  test('saving a new template as draft persists immediately without requiring a cover', async () => {
    const user = userEvent.setup()
    renderPage()
    const dialog = await openFilledTemplate(user)
    await user.click(within(dialog).getByRole('button', { name: 'Save draft' }))
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
    expect(put).toHaveBeenCalledOnce()
    expect(put.mock.calls[0][1].image.templates[0]).toMatchObject({
      status: 'draft',
      coverAssetId: null,
    })
    expect(screen.getByRole('button', { name: 'Save content' })).toBeDisabled()
  })
  test.each([200, 503, 409])(
    'HTTP %s keeps template edits and cover visible in the dialog',
    async (status) => {
      const user = userEvent.setup()
      const initial = content()
      initial.image.templates = [existingTemplate()]
      get.mockResolvedValue({ data: { success: true, data: initial } })
      const message =
        status === 409
          ? 'Creation content changed; reload before saving'
          : 'Creation content storage is unavailable'
      if (status === 200) {
        put.mockResolvedValueOnce({ data: { success: false, message } })
      } else {
        put.mockRejectedValueOnce({
          response: { status, data: { success: false, message } },
        })
      }
      renderPage()
      await user.click(
        await screen.findByRole('button', {
          name: 'Edit template: Existing template',
        })
      )
      const dialog = await screen.findByRole('dialog')
      const prompt = within(dialog).getByLabelText('Prompt · Chinese')
      await user.type(prompt, ' with my changes')
      await user.click(within(dialog).getByRole('button', { name: 'Publish' }))
      expect(await within(dialog).findByText(message)).toBeVisible()
      expect(prompt).toHaveValue('Keep this prompt with my changes')
      expect(within(dialog).getByAltText('Template cover')).toHaveAttribute(
        'src',
        `/api/creation/assets/${coverAssetId}`
      )
      expect(
        within(dialog).getByRole('button', { name: 'Publish' })
      ).toBeEnabled()
      expect(
        within(dialog).getByRole('button', { name: 'Move to draft' })
      ).toBeEnabled()
    }
  )
  test('a failed request can retry and subsequent edits use the returned revision without duplicate templates', async () => {
    const user = userEvent.setup()
    const initial = content()
    initial.image.templates = [existingTemplate()]
    get.mockResolvedValue({ data: { success: true, data: initial } })
    put.mockRejectedValueOnce(new Error('Network unavailable'))
    renderPage()
    await user.click(
      await screen.findByRole('button', {
        name: 'Edit template: Existing template',
      })
    )
    let dialog = await screen.findByRole('dialog')
    await user.click(within(dialog).getByRole('button', { name: 'Publish' }))
    expect(await within(dialog).findByText('Network unavailable')).toBeVisible()
    await user.click(within(dialog).getByRole('button', { name: 'Publish' }))
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
    await user.click(
      screen.getByRole('button', { name: 'Edit template: Existing template' })
    )
    dialog = await screen.findByRole('dialog')
    await user.click(
      within(dialog).getByRole('button', { name: 'Move to draft' })
    )
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
    expect(put).toHaveBeenCalledTimes(3)
    expect(put.mock.calls[1][1].revision).toBe('0')
    expect(put.mock.calls[2][1]).toMatchObject({
      revision: 'next',
      image: { templates: [{ id: 'existing', status: 'draft' }] },
    })
    expect(put.mock.calls[2][1].image.templates).toHaveLength(1)
  })
  test('a pending publish prevents repeat submission, editing, upload, cancellation and page save or reload', async () => {
    const user = userEvent.setup()
    const initial = content()
    initial.image.templates = [existingTemplate()]
    get.mockResolvedValue({ data: { success: true, data: initial } })
    let resolvePut: (value: {
      data: { success: boolean; data: CreationContent }
    }) => void = () => undefined
    put.mockImplementationOnce(
      () =>
        new Promise((resolve) => {
          resolvePut = resolve
        })
    )
    renderPage()
    const edit = await screen.findByRole('button', {
      name: 'Edit template: Existing template',
    })
    const pageSave = screen.getByRole('button', { name: 'Save content' })
    const pageReload = screen.getByRole('button', { name: 'Reload content' })
    await user.click(edit)
    const dialog = await screen.findByRole('dialog')
    const editorForm = within(dialog).getByRole('form', {
      name: 'Edit creation template',
    })
    fireEvent.submit(editorForm)
    fireEvent.submit(editorForm)
    expect(
      await within(dialog).findByRole('button', { name: 'Publishing...' })
    ).toBeDisabled()
    expect(
      within(dialog).getByRole('button', { name: 'Move to draft' })
    ).toBeDisabled()
    expect(
      within(dialog).getByRole('button', { name: 'Cancel' })
    ).toBeDisabled()
    expect(within(dialog).getByLabelText('Prompt · Chinese')).toBeDisabled()
    expect(within(dialog).getByLabelText('Template cover')).toBeDisabled()
    await waitFor(() => expect(put).toHaveBeenCalledOnce())
    expect(pageSave).toBeDisabled()
    expect(pageReload).toBeDisabled()
    await user.keyboard('{Enter}{Escape}')
    expect(screen.getByRole('dialog')).toBeVisible()
    expect(put).toHaveBeenCalledOnce()
    resolvePut({
      data: { success: true, data: { ...initial, revision: 'after-publish' } },
    })
    await waitFor(() =>
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    )
  })
  test('invalid unsaved content blocks direct template saving without dropping fields', async () => {
    const user = userEvent.setup()
    renderPage()
    await user.click(
      await screen.findByRole('button', { name: 'Add category' })
    )
    const dialog = await openFilledTemplate(user)
    await user.click(within(dialog).getByRole('button', { name: 'Save draft' }))
    expect(
      await within(dialog).findByText(
        'Other unsaved content is incomplete. Complete required fields on this page before saving the template. Your template edits are retained.'
      )
    ).toBeVisible()
    expect(within(dialog).getByLabelText('Prompt · Chinese')).toHaveValue(
      'A red cup under soft light'
    )
    expect(put).not.toHaveBeenCalled()
  })
})
