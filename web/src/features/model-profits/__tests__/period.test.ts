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
import { describe, expect, it } from 'vitest'

import { parseProfitPeriod, profitPreset } from '../lib/period'

describe('Shanghai profit reporting periods', () => {
  it('maps an inclusive calendar range to exclusive Shanghai midnight regardless of browser timezone', () => {
    expect(
      parseProfitPeriod({
        start: '2026-10-01',
        end: '2026-10-21',
        model: ' exact-model ',
      })
    ).toEqual({
      start_timestamp: Date.parse('2026-10-01T00:00:00+08:00') / 1000,
      end_timestamp: Date.parse('2026-10-22T00:00:00+08:00') / 1000,
      model_name: 'exact-model',
    })
  })
  it('uses Shanghai day boundaries for today, yesterday, week and month presets', () => {
    const now = Date.parse('2026-10-20T16:05:00Z')
    expect(profitPreset('today', now)).toEqual({
      start: '2026-10-21',
      end: '2026-10-21',
      model: '',
    })
    expect(profitPreset('yesterday', now)).toEqual({
      start: '2026-10-20',
      end: '2026-10-20',
      model: '',
    })
    expect(profitPreset('week', now).start).toBe('2026-10-15')
    expect(profitPreset('month', now).start).toBe('2026-10-01')
  })
  it.each([
    { start: '2026-02-30', end: '2026-03-01', model: '' },
    { start: '', end: '2026-10-21', model: '' },
    { start: '2026-10-22', end: '2026-10-21', model: '' },
  ])(
    'rejects missing, nonexistent or reversed dates: $start / $end',
    (value) => {
      expect(parseProfitPeriod(value)).toBeNull()
    }
  )
})
