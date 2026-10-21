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
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

import {
  profitPreset,
  type ProfitPeriod,
  type ProfitPreset,
} from '../lib/period'

export function ProfitFilters(props: {
  value: ProfitPeriod
  onChange: (value: ProfitPeriod) => void
  onApply: () => void
  loading: boolean
  invalid: boolean
}) {
  const { t } = useTranslation()
  const presets: { value: ProfitPreset; label: string }[] = [
    { value: 'today', label: t('Today') },
    { value: 'yesterday', label: t('Yesterday') },
    { value: 'week', label: t('Last 7 days') },
    { value: 'month', label: t('Month to date') },
  ]
  return (
    <div className='space-y-3 rounded-xl border p-4'>
      <div className='flex flex-wrap items-center gap-2'>
        {presets.map((preset) => (
          <Button
            key={preset.value}
            size='sm'
            variant='outline'
            onClick={() =>
              props.onChange({
                ...profitPreset(preset.value),
                model: props.value.model,
              })
            }
          >
            {preset.label}
          </Button>
        ))}
        <span className='text-muted-foreground text-xs'>
          {t('Calendar dates in Asia/Shanghai (UTC+8)')}
        </span>
      </div>
      <form
        className='grid items-end gap-3 sm:grid-cols-2 xl:grid-cols-[1fr_1fr_2fr_auto]'
        onSubmit={(event) => {
          event.preventDefault()
          props.onApply()
        }}
      >
        <div className='space-y-2'>
          <Label htmlFor='model-profit-start'>{t('Start date')}</Label>
          <Input
            id='model-profit-start'
            type='date'
            value={props.value.start}
            aria-invalid={props.invalid}
            onChange={(event) =>
              props.onChange({ ...props.value, start: event.target.value })
            }
          />
        </div>
        <div className='space-y-2'>
          <Label htmlFor='model-profit-end'>{t('End date (inclusive)')}</Label>
          <Input
            id='model-profit-end'
            type='date'
            value={props.value.end}
            aria-invalid={props.invalid}
            onChange={(event) =>
              props.onChange({ ...props.value, end: event.target.value })
            }
          />
        </div>
        <div className='space-y-2'>
          <Label htmlFor='model-profit-model'>{t('Exact model name')}</Label>
          <Input
            id='model-profit-model'
            value={props.value.model}
            placeholder={t('All models')}
            onChange={(event) =>
              props.onChange({ ...props.value, model: event.target.value })
            }
          />
        </div>
        <Button type='submit' disabled={props.loading || props.invalid}>
          {t('Apply filters')}
        </Button>
      </form>
    </div>
  )
}
