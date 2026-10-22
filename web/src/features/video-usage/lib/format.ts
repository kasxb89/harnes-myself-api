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
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import type { VideoUser } from '../schema'

export function userOptionLabel(user: VideoUser): string {
  const name = user.display_name || user.username
  return name ? `${name} (#${user.id})` : `#${user.id}`
}
export function useVideoFormat() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  return useMemo(
    () => ({
      count: (value: number) => formatNumber(value, locale),
      duration: (value: number | null) =>
        value === null
          ? t('Unknown')
          : t('{{value}} s', { value: formatNumber(value, locale) }),
      time: (value: number | null) =>
        value === null
          ? t('Unknown')
          : new Intl.DateTimeFormat(locale, {
              dateStyle: 'medium',
              timeStyle: 'medium',
            }).format(new Date(value * 1000)),
    }),
    [locale, t]
  )
}
