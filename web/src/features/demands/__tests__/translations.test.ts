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
import { createInstance } from 'i18next'
import { expect, test } from 'vitest'

import en from '@/i18n/locales/en.json'
import zh from '@/i18n/locales/zh.json'

test('Chinese approval controls resolve their labels after switching interface language', async () => {
  const instance = createInstance()
  await instance.init({
    lng: 'en',
    fallbackLng: 'en',
    nsSeparator: false,
    resources: { en, zhCN: zh },
  })
  await instance.changeLanguage('zhCN')

  const labels = {
    'Community approval status': '需求审核状态',
    'All approval statuses': '全部审核状态',
    'Pending approval': '待审核',
    'Approved listing': '已通过审核',
    'Rejected listing': '已驳回',
    'Approve listing': '通过审核',
    'Reject listing': '驳回需求',
    'Expected delivery date': '预期交付日',
    'Invalid expected delivery date': '预期交付日无效',
    'Administrator approval is required before acceptance':
      '管理员审核通过后才能接单',
  }
  expect(
    Object.fromEntries(Object.keys(labels).map((key) => [key, instance.t(key)]))
  ).toEqual(labels)
})
