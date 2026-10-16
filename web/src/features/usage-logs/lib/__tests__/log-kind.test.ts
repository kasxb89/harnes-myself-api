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
import { describe, expect, test } from 'vitest'

import { formatLogQuota } from '@/lib/format'

import { logAmountLabel, logKindLabel } from '../format'

// 传回键本身，断言的是稳定的翻译键而不是某一种语言的文案。
const t = (key: string): string => key

describe('logKindLabel', () => {
  test('注册赠送与充值同属 Top-up 类型，但按内容键区分开', () => {
    expect(
      logKindLabel(
        { type: 1 },
        { content_parts: [{ key: 'New user sign-up bonus {{quota}}' }] },
        t
      )
    ).toBe('Sign-up bonus')
  })

  test('兑换码入账显示为兑换码', () => {
    expect(
      logKindLabel(
        { type: 1 },
        {
          content_parts: [
            {
              key: 'Topped up {{quota}} with a redemption code, code ID {{id}}',
            },
          ],
        },
        t
      )
    ).toBe('Redemption code')
  })

  test('没有可识别的内容键时回落到日志类型标签', () => {
    expect(logKindLabel({ type: 2 }, null, t)).toBe('Consume')
    expect(logKindLabel({ type: 1 }, { content_parts: [] }, t)).toBe('Top-up')
  })
})

describe('logAmountLabel', () => {
  test('充值行自身 quota 为 0，金额取内容参数里已格式化的值', () => {
    expect(
      logAmountLabel(
        { quota: 0 },
        {
          content_parts: [
            {
              key: '{{provider}} top-up succeeded, amount added: {{quota}}, payment amount: {{amount}}',
              params: { provider: 'WeChat Pay', quota: '¥1.00', amount: '1.00' },
            },
          ],
        }
      )
    ).toBe('¥1.00')
  })

  test('消费行没有 quota 参数，回落到该行自己的配额', () => {
    expect(logAmountLabel({ quota: 500000 }, { content_parts: [] })).toBe(
      formatLogQuota(500000)
    )
  })
})
