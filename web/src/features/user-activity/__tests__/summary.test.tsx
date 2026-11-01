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
import { cleanup, render, screen } from '@testing-library/react'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, expect, it } from 'vitest'

import en from '@/i18n/locales/en.json'
import fr from '@/i18n/locales/fr.json'
import ja from '@/i18n/locales/ja.json'
import ru from '@/i18n/locales/ru.json'
import vi from '@/i18n/locales/vi.json'
import zhTW from '@/i18n/locales/zh-TW.json'
import zh from '@/i18n/locales/zh.json'

import { ActivitySummary } from '../summary'

const resources = { en, fr, ja, ru, vi, zhCN: zh, zhTW }
const titles = [
  'Currently online users',
  'Active foreground users',
  'Background or idle users',
  'Client sessions started in range',
  'Actual model calls in range',
] as const

afterEach(cleanup)

it('separates Beijing daily client users from calling users and keeps old servers unknown', async () => {
  const i18n = createInstance()
  await i18n.init({
    lng: 'en',
    resources,
    keySeparator: false,
    nsSeparator: false,
  })
  const summary = {
    server_time: 1,
    heartbeat_interval_seconds: 30,
    online_timeout_seconds: 90,
    online_users: 1,
    foreground_users: 1,
    started_sessions: 0,
    active_users: 0,
    online_seconds: 0,
    foreground_seconds: 0,
    model_calls: 0,
    model_errors: 0,
    client_active_users_today: 12,
    calling_users_today: 7,
  }
  const view = render(
    <I18nextProvider i18n={i18n}>
      <ActivitySummary
        summary={summary}
        loading={false}
        error={false}
        history={false}
      />
    </I18nextProvider>
  )
  expect(screen.getByText('Client active users today')).toBeVisible()
  expect(screen.getByText('Calling users today')).toBeVisible()
  expect(screen.getByText('12')).toBeVisible()
  expect(screen.getByText('7')).toBeVisible()
  view.rerender(
    <I18nextProvider i18n={i18n}>
      <ActivitySummary
        summary={{
          ...summary,
          client_active_users_today: undefined,
          calling_users_today: undefined,
        }}
        loading={false}
        error={false}
        history={false}
      />
    </I18nextProvider>
  )
  expect(screen.queryByText('12')).not.toBeInTheDocument()
  expect(screen.getAllByText('Not collected').length).toBeGreaterThanOrEqual(2)
})

it.each(Object.keys(resources) as (keyof typeof resources)[])(
  'renders all five summary titles using the actual %s translations without English fallback',
  async (language) => {
    const i18n = createInstance()
    await i18n.init({
      lng: language,
      fallbackLng: 'en',
      keySeparator: false,
      nsSeparator: false,
      resources,
    })
    render(
      <I18nextProvider i18n={i18n}>
        <ActivitySummary loading={false} error={false} />
      </I18nextProvider>
    )
    for (const key of titles) {
      const translated = resources[language].translation[key]
      expect(screen.getByText(translated, { exact: true })).toBeVisible()
      if (language !== 'en') {
        expect(screen.queryByText(key, { exact: true })).not.toBeInTheDocument()
      }
    }
  }
)
