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
import { expect, it } from 'vitest'

import { toIntlLocale } from '@/i18n/languages'

import { formatProfitCny, formatProfitMargin } from '../lib/format'

it.each(['en', 'zhCN', 'zhTW', 'fr', 'ru', 'ja', 'vi', 'invalid_tag_!'])(
  'formats CNY independently of the display currency with safe language %s',
  (language) => {
    const locale = toIntlLocale(language)
    expect(formatProfitCny(1234.56789, locale)).toContain('56789')
    expect(formatProfitCny(null, locale)).toBe('—')
    expect(formatProfitCny(0.000000001, locale)).toMatch(/^</)
    expect(formatProfitCny(-0.000000001, locale)).toMatch(/^>/)
    expect(formatProfitMargin(null, locale)).toBe('—')
    expect(formatProfitMargin(-0.00001993, locale)).toMatch(/[-−]/)
    expect(formatProfitMargin(-0.00001993, locale)).not.toMatch(/^[-−]0[%\s]/)
    expect(formatProfitMargin(0.000000001, locale)).toMatch(/^</)
    expect(formatProfitMargin(-0.000000001, locale)).toMatch(/^>/)
  }
)
