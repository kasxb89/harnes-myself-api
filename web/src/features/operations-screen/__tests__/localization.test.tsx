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
import { act, render, screen, within } from '@testing-library/react'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { expect, it } from 'vitest'

import { usageLogSchema } from '@/features/usage-logs/data/schema'
import en from '@/i18n/locales/en.json'
import fr from '@/i18n/locales/fr.json'
import ja from '@/i18n/locales/ja.json'
import ru from '@/i18n/locales/ru.json'
import vi from '@/i18n/locales/vi.json'
import zhTW from '@/i18n/locales/zh-TW.json'
import zh from '@/i18n/locales/zh.json'

import { OperationsLogsPanel } from '../components/logs-panel'
import { operationsTabs } from '../policy'

const resources = { en, fr, ja, ru, vi, zhCN: zh, zhTW }
const labels = 'Recent consumption records'
const row = usageLogSchema.parse({
  id: 1,
  user_id: 7,
  type: 2,
  created_at: 1792810800,
  content: '',
  username: 'fixture-operator',
  model_name: 'fixture-model',
  prompt_tokens: 12345,
  completion_tokens: 0,
  use_time: 1,
})
const snapshot = {
  tab: 'usage' as const,
  window: { start_timestamp: 1792810500, end_timestamp: 1792810800 },
  logs: { total: 12345, items: [row] },
  stats: { quota: 0, rpm: 12345, tpm: 0 },
}

it.each([
  ['en', '12,345', 'en'],
  ['zhCN', '12,345', 'zhCN'],
  ['zhTW', '12,345', 'zhTW'],
  ['fr', '12 345', 'fr'],
  ['ru', '12 345', 'ru'],
  ['ja', '12,345', 'ja'],
  ['vi', '12.345', 'vi'],
  ['invalid_locale!', '12,345', 'en'],
] as const)(
  'renders actual %s labels, numbers and timestamp with safe locale fallback',
  async (language, number, resolved) => {
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
        <OperationsLogsPanel snapshot={snapshot} />
      </I18nextProvider>
    )
    const table = screen.getByRole('table', {
      name: resources[resolved].translation[labels],
    })
    const record = within(table).getAllByRole('row')[1]
    expect(within(record).getByRole('cell', { name: number })).toBeVisible()
    expect(record.querySelector('time')).toHaveAttribute(
      'dateTime',
      '2026-10-24T03:00:00.000Z'
    )
    expect(record.querySelector('time')).toHaveTextContent(
      /\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} [+-]\d{2}:\d{2}/
    )
  }
)
it('updates existing rendered labels and number separators across language changes while preserving the observation instant', async () => {
  const i18n = createInstance()
  await i18n.init({
    lng: 'en',
    fallbackLng: 'en',
    keySeparator: false,
    nsSeparator: false,
    resources,
  })
  render(
    <I18nextProvider i18n={i18n}>
      <OperationsLogsPanel snapshot={snapshot} />
    </I18nextProvider>
  )
  expect(
    screen.getByRole('table', { name: en.translation[labels] })
  ).toBeVisible()
  await act(async () => {
    await i18n.changeLanguage('fr')
  })
  const french = screen.getByRole('table', { name: fr.translation[labels] })
  expect(
    within(within(french).getAllByRole('row')[1]).getByRole('cell', {
      name: '12 345',
    })
  ).toBeVisible()
  expect(french.querySelector('time')).toHaveAttribute(
    'dateTime',
    '2026-10-24T03:00:00.000Z'
  )
  expect(
    screen.queryByRole('table', { name: en.translation[labels] })
  ).not.toBeInTheDocument()
  await act(async () => {
    await i18n.changeLanguage('zhTW')
  })
  const chinese = screen.getByRole('table', { name: zhTW.translation[labels] })
  expect(
    within(within(chinese).getAllByRole('row')[1]).getByRole('cell', {
      name: '12,345',
    })
  ).toBeVisible()
  expect(chinese.querySelector('time')).toHaveAttribute(
    'dateTime',
    '2026-10-24T03:00:00.000Z'
  )
})

it.each(Object.entries(resources))(
  'has actual translations for every dynamic operations tab in %s',
  (language, resource) => {
    for (const tab of operationsTabs) {
      const translations: Record<string, string> = resource.translation
      expect(Object.hasOwn(translations, tab.labelKey)).toBe(true)
      expect(translations[tab.labelKey]?.trim()).toBeTruthy()
      if (
        language !== 'en' &&
        ['Concurrency', 'Call usage', 'API errors'].includes(tab.labelKey)
      ) {
        expect(translations[tab.labelKey]).not.toBe(tab.labelKey)
      }
    }
  }
)
it('uses the separate Chinese image and video tab labels rather than dynamic English fallback keys', () => {
  const translations: Record<string, string> = zh.translation
  expect(operationsTabs.map((tab) => translations[tab.labelKey])).toEqual([
    '利润',
    '并发',
    '调用使用',
    '接口报错',
    '图片',
    '视频',
    '用户活跃',
  ])
})
