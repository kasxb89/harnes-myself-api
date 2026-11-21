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
import LanguageDetector from 'i18next-browser-languagedetector'
import { afterEach, expect, it, vi } from 'vitest'

import {
  convertDetectedLanguage,
  INTERFACE_LANGUAGE_OPTIONS,
} from '../languages'
import { createLocaleBackend, type LocaleLoaders } from '../locale-backend'

function loaders() {
  return {
    en: vi.fn(async () => ({
      default: { translation: { 'Sign in': 'Sign in' } },
    })),
    zhCN: vi.fn(async () => ({
      default: { translation: { 'Sign in': '登录' } },
    })),
    fr: vi.fn(async () => ({
      default: { translation: { 'Sign in': 'Connexion' } },
    })),
    ru: vi.fn(async () => ({
      default: { translation: { 'Sign in': 'Войти' } },
    })),
    ja: vi.fn(async () => ({
      default: { translation: { 'Sign in': 'ログイン' } },
    })),
    vi: vi.fn(async () => ({
      default: { translation: { 'Sign in': 'Đăng nhập' } },
    })),
    zhTW: vi.fn(async () => ({
      default: { translation: { 'Sign in': '登入' } },
    })),
  } satisfies LocaleLoaders
}
const fallback = {
  en: {
    translation: { 'Sign in': 'Sign in', 'Fallback only': 'English fallback' },
  },
}
function options() {
  return {
    fallbackLng: 'en',
    supportedLngs: INTERFACE_LANGUAGE_OPTIONS.map((item) => item.code),
    load: 'currentOnly' as const,
    resources: { en: { translation: { ...fallback.en.translation } } },
    partialBundledLanguages: true,
    nsSeparator: false as const,
    interpolation: { escapeValue: false },
  }
}
afterEach(() => localStorage.clear())

it('loads only the detected Traditional Chinese locale while retaining the bundled English fallback', async () => {
  localStorage.setItem('i18nextLng', 'zhTW')
  const lazy = loaders()
  const i18n = createInstance()
    .use(LanguageDetector)
    .use(createLocaleBackend(lazy))
  await i18n.init({
    ...options(),
    detection: { order: ['localStorage'], caches: [], convertDetectedLanguage },
  })
  expect(i18n.t('Sign in')).toBe('登入')
  expect(i18n.t('Fallback only')).toBe('English fallback')
  expect(lazy.zhTW).toHaveBeenCalledTimes(1)
  for (const key of Object.keys(lazy).filter(
    (key) => key !== 'zhTW'
  ) as (keyof typeof lazy)[]) {
    expect(lazy[key]).not.toHaveBeenCalled()
  }
})
it('keeps all seven choices working and reuses previously loaded locale resources', async () => {
  const lazy = loaders()
  const i18n = createInstance().use(createLocaleBackend(lazy))
  await i18n.init({ ...options(), lng: 'en' })
  for (const language of INTERFACE_LANGUAGE_OPTIONS) {
    await i18n.changeLanguage(language.code)
    expect(i18n.resolvedLanguage).toBe(language.code)
    expect(i18n.t('Sign in')).not.toBe('')
  }
  await i18n.changeLanguage('fr')
  expect(lazy.fr).toHaveBeenCalledTimes(1)
  expect(lazy.en).not.toHaveBeenCalled()
})
it('uses English when the selected chunk fails and permits a later successful language choice', async () => {
  const lazy = loaders()
  lazy.zhCN.mockRejectedValue(new Error('synthetic missing locale chunk'))
  const i18n = createInstance().use(createLocaleBackend(lazy))
  await i18n.init({ ...options(), lng: 'zhCN' })
  expect(i18n.t('Sign in')).toBe('Sign in')
  expect(i18n.t('Fallback only')).toBe('English fallback')
  expect(lazy.zhCN).toHaveBeenCalledTimes(1)
  await i18n.changeLanguage('fr')
  expect(i18n.t('Sign in')).toBe('Connexion')
})
it('falls back to English for an unsupported language without importing another language', async () => {
  const lazy = loaders()
  const i18n = createInstance().use(createLocaleBackend(lazy))
  await i18n.init({ ...options(), lng: 'unsupported' })
  expect(i18n.t('Sign in')).toBe('Sign in')
  for (const loader of Object.values(lazy)) {
    expect(loader).not.toHaveBeenCalled()
  }
})
