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
import { act, cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, expect, it, vi } from 'vitest'

import en from '@/i18n/locales/en.json'
import fr from '@/i18n/locales/fr.json'
import ja from '@/i18n/locales/ja.json'
import ru from '@/i18n/locales/ru.json'
import viLocale from '@/i18n/locales/vi.json'
import zhTW from '@/i18n/locales/zh-TW.json'
import zh from '@/i18n/locales/zh.json'
import { api } from '@/lib/api'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { VideoUsagePage } from '..'
import { videoPayload } from './fixtures'

const clients: QueryClient[] = []
afterEach(() => {
  cleanup()
  clients.splice(0).forEach((client) => client.clear())
  useAuthStore.getState().auth.reset()
  vi.restoreAllMocks()
})
it.each([
  ['en', en],
  ['zhCN', zh],
  ['zhTW', zhTW],
  ['fr', fr],
  ['ja', ja],
  ['ru', ru],
  ['vi', viLocale],
] as const)(
  'changing to %s updates source and timing labels without invalid locale formatting',
  async (language, resources) => {
    const labels = resources.translation as Record<string, string>
    expect(labels['Video client usage']).toBeTruthy()
    expect(labels['Timing missing']).toBeTruthy()
    expect(labels['Video trends']).toBeTruthy()
    expect(labels['Provider (current settings)']).toBeTruthy()
    const i18n = createInstance()
    await i18n.init({
      lng: 'en',
      fallbackLng: 'en',
      keySeparator: false,
      nsSeparator: false,
      resources: { en, [language]: resources },
    })
    useAuthStore
      .getState()
      .auth.setUser({ id: 1, username: 'admin', role: ROLE.ADMIN })
    vi.spyOn(api, 'get').mockResolvedValue({
      data: { success: true, data: videoPayload },
    })
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    })
    clients.push(client)
    render(
      <I18nextProvider i18n={i18n}>
        <QueryClientProvider client={client}>
          <VideoUsagePage />
        </QueryClientProvider>
      </I18nextProvider>
    )
    await screen.findByRole('table', { name: 'Current video task records' })
    await act(async () => {
      await i18n.changeLanguage(language)
    })
    expect(screen.getByText(labels['Video client usage'])).toBeVisible()
    expect(
      screen.getByRole('combobox', { name: labels['Client source'] })
    ).toBeVisible()
    await userEvent.click(
      screen.getByRole('tab', { name: labels['Task details'] })
    )
    const row = screen.getByRole('row', { name: /task-102/ })
    expect(within(row).getByText(labels['Timing missing'])).toBeVisible()
    const declared = screen.getByRole('row', { name: /task-101/ })
    expect(
      within(declared).getByText(labels['Desktop client declaration'])
    ).toBeVisible()
    await userEvent.click(
      screen.getByRole('tab', { name: labels['Video trends'] })
    )
    expect(
      await screen.findByRole('img', { name: labels['Video HTTP ingress QPS'] })
    ).toBeVisible()
    expect(
      screen.getByText(
        labels[
          'Only the time range applies. Model, user, client source, channel and provider filters do not apply to this chart.'
        ]
      )
    ).toBeVisible()
  }
)
