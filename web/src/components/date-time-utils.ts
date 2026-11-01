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
import { enUS, fr, ja, ru, vi, zhCN, zhTW } from 'react-day-picker/locale'

import {
  convertDetectedLanguage,
  normalizeInterfaceLanguage,
} from '@/i18n/languages'
import dayjs from '@/lib/dayjs'

export function calendarLocale(language: string) {
  return (
    { en: enUS, zhCN, zhTW, fr, ru, ja, vi }[
      normalizeInterfaceLanguage(convertDetectedLanguage(language)) as
        | 'en'
        | 'zhCN'
        | 'zhTW'
        | 'fr'
        | 'ru'
        | 'ja'
        | 'vi'
    ] ?? enUS
  )
}

export function dateInputValue(date?: Date): string {
  return date && Number.isFinite(date.getTime())
    ? dayjs(date).format('YYYY-MM-DD')
    : ''
}

export function timeInputValue(date?: Date, fallback = '00:00:00'): string {
  return date && Number.isFinite(date.getTime())
    ? dayjs(date).format('HH:mm:ss')
    : fallback
}

export function parseDateTime(
  date: string,
  time = '00:00:00'
): Date | undefined {
  if (
    !/^\d{4}-\d{2}-\d{2}$/.test(date) ||
    !/^\d{2}:\d{2}(:\d{2})?$/.test(time)
  ) {
    return undefined
  }
  const expectedTime = time.length === 5 ? `${time}:00` : time
  const parsed = new Date(`${date}T${expectedTime}`)
  if (
    !Number.isFinite(parsed.getTime()) ||
    dateInputValue(parsed) !== date ||
    timeInputValue(parsed) !== expectedTime
  ) {
    return undefined
  }
  return parsed
}

export function browserTimeZone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone
}
