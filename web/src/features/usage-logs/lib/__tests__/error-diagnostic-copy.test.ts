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
import { describe, expect, test } from 'vitest'

import en from '@/i18n/locales/en.json'
import fr from '@/i18n/locales/fr.json'
import ja from '@/i18n/locales/ja.json'
import ru from '@/i18n/locales/ru.json'
import vi from '@/i18n/locales/vi.json'
import zhTW from '@/i18n/locales/zh-TW.json'
import zh from '@/i18n/locales/zh.json'

import { getErrorDiagnosticCopy } from '../error-diagnostic-copy'
import type { ErrorReason } from '../error-diagnostics'

const chineseTitles = {
  upstream_balance: '上游账户余额不足',
  user_balance: '用户余额或套餐额度不足',
  token_quota: '令牌额度检查或预扣失败',
  authentication: '身份认证失败',
  permission: '请求被拒绝',
  rate_limit: '触发请求限流',
  concurrency_limit: '并发请求超过限制',
  model_unavailable: '模型不存在或不可用',
  invalid_request: '请求参数不符合要求',
  context_length: '上下文超过模型限制',
  content_policy: '请求触发内容安全限制',
  payload_too_large: '请求体或文件过大',
  unsupported_operation: '接口或操作不受支持',
  no_available_channel: '没有可用渠道',
  timeout: '请求超时',
  connection: '网络连接失败',
  dns: 'DNS 解析失败',
  tls: 'TLS 证书或握手失败',
  upstream_unavailable: '服务暂时不可用',
  server_error: '服务内部错误',
  invalid_response: '返回内容为空或无法解析',
  payment_required: '需要付费或开通套餐',
  not_found: '请求的资源不存在',
  channel_configuration: '渠道配置错误',
  billing_configuration: '模型计费配置错误',
  request_conversion: '请求转换或编码失败',
  internal_error: '网关内部处理失败',
  request_cancelled: '请求已取消',
  response_read_failed: '读取响应失败',
  token_count_failed: '请求令牌计算失败',
  unknown: '尚未识别具体原因',
} satisfies Record<ErrorReason, string>

const reasons = Object.keys(chineseTitles) as ErrorReason[]
const resources = { en, zh, 'zh-TW': zhTW, fr, ru, ja, vi }

describe('error diagnostic copy', () => {
  test('shows a specific Chinese title for every supported reason', async () => {
    const i18n = createInstance()
    await i18n.init({ lng: 'zh', resources, keySeparator: false })

    for (const reason of reasons) {
      const copy = getErrorDiagnosticCopy(reason, i18n.t)
      expect(copy.title).toBe(chineseTitles[reason])
      expect(copy.description).toMatch(/[\u4e00-\u9fff]/)
    }
  })

  test.each(Object.keys(resources))(
    'has complete diagnostics and source labels in %s without fallback',
    async (language) => {
      const missingKeys: string[] = []
      const i18n = createInstance()
      i18n.on('missingKey', (_languages, _namespace, key) =>
        missingKeys.push(key)
      )
      await i18n.init({
        lng: language,
        resources,
        fallbackLng: false,
        keySeparator: false,
        saveMissing: true,
      })

      for (const reason of reasons) {
        const copy = getErrorDiagnosticCopy(reason, i18n.t)
        expect(copy.title.trim()).not.toBe('')
        expect(copy.description.trim()).not.toBe('')
      }
      for (const key of [
        'Error Source',
        'Upstream service',
        'Local gateway',
        'Source unconfirmed',
      ]) {
        expect(i18n.exists(key)).toBe(true)
      }
      expect(missingKeys).toEqual([])
    }
  )

  test('explains an unconfirmed server error without assigning gateway or upstream blame', async () => {
    const i18n = createInstance()
    await i18n.init({ lng: 'zh', resources, keySeparator: false })

    expect(getErrorDiagnosticCopy('server_error', i18n.t)).toEqual({
      title: '服务内部错误',
      description:
        '接口返回了服务器错误状态。仅凭状态码无法确定故障位于网关还是上游，请结合原始错误和请求ID排查。',
    })
  })

  test('distinguishes quota checks from balance and preserves uncertain acceptance', async () => {
    const i18n = createInstance()
    await i18n.init({ lng: 'zh', resources, keySeparator: false })

    expect(
      getErrorDiagnosticCopy('user_balance', i18n.t).description
    ).toContain('用户')
    expect(getErrorDiagnosticCopy('token_quota', i18n.t).description).toContain(
      '不能确定是用户资金不足'
    )
    for (const reason of [
      'timeout',
      'connection',
      'request_cancelled',
      'response_read_failed',
    ] as const) {
      expect(getErrorDiagnosticCopy(reason, i18n.t).description).toContain(
        '受理状态'
      )
    }
    expect(getErrorDiagnosticCopy('unknown', i18n.t).description).toContain(
      '当前记录不足以确定具体原因'
    )
    expect(getErrorDiagnosticCopy('unknown', i18n.t).description).toContain(
      '原始响应'
    )
  })
})
