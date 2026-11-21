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
import type { BackendModule } from 'i18next'

import {
  INTERFACE_LANGUAGE_OPTIONS,
  type InterfaceLanguageCode,
} from './languages'

type LocaleModule = { default: { translation: Record<string, string> } }
type LocaleLoader = () => Promise<LocaleModule>
export type LocaleLoaders = Readonly<
  Record<InterfaceLanguageCode, LocaleLoader>
>

const localeLoaders: LocaleLoaders = {
  en: () => import('./locales/en.json'),
  zhCN: () => import('./locales/zh.json'),
  fr: () => import('./locales/fr.json'),
  ru: () => import('./locales/ru.json'),
  ja: () => import('./locales/ja.json'),
  vi: () => import('./locales/vi.json'),
  zhTW: () => import('./locales/zh-TW.json'),
}

/** i18next owns selection, fallback, in-flight deduplication and loaded-language caching. */
export function createLocaleBackend(
  loaders: LocaleLoaders = localeLoaders
): BackendModule {
  return {
    type: 'backend',
    init: () => {},
    async read(language, namespace): Promise<Record<string, string>> {
      const option = INTERFACE_LANGUAGE_OPTIONS.find(
        (item) => item.code === language
      )
      if (!option || namespace !== 'translation') return {}
      // The installed i18next BackendConnector accepts a two-argument Promise
      // backend and handles rejection without retrying a missing locale chunk.
      const module = await loaders[option.code]()
      return module.default.translation
    },
  }
}
