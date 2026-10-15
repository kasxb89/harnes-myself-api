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
import { afterEach, beforeEach, expect, test } from 'vitest'

import {
  DEFAULT_CURRENCY_CONFIG,
  useSystemConfigStore,
} from '@/stores/system-config-store'

import { formatTopUpAmount } from '../billing'

/** 200 CNY converts to this much quota at 500000 quota/USD and a 7.3 rate. */
const TWO_HUNDRED_YUAN_AS_QUOTA = 13_698_630

/** The same 200 CNY when the provider records a USD figure instead. */
const TWO_HUNDRED_YUAN_AS_USD = 200 / 7.3

beforeEach(() => {
  useSystemConfigStore.getState().setConfig({
    currency: {
      ...DEFAULT_CURRENCY_CONFIG,
      quotaDisplayType: 'CNY',
      usdExchangeRate: 7.3,
      quotaPerUnit: 500_000,
    },
  })
})

afterEach(() => {
  useSystemConfigStore
    .getState()
    .setConfig({ currency: { ...DEFAULT_CURRENCY_CONFIG } })
})

test('a WeChat top-up shows the money paid, not its quota read as dollars', () => {
  expect(
    formatTopUpAmount({
      amount: TWO_HUNDRED_YUAN_AS_QUOTA,
      payment_provider: 'wechat',
    })
  ).toBe('¥200')
})

test('the same top-up reads the same through a provider that records USD', () => {
  expect(
    formatTopUpAmount({
      amount: TWO_HUNDRED_YUAN_AS_USD,
      payment_provider: 'epay',
    })
  ).toBe('¥200')
})
