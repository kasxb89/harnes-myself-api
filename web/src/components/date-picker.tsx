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
import { CalendarDays, ChevronDown } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { calendarLocale, dateInputValue } from '@/components/date-time-utils'
import { Button } from '@/components/ui/button'
import { Calendar } from '@/components/ui/calendar'
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from '@/components/ui/popover'

type DatePickerProps = {
  selected: Date | undefined
  onSelect: (date: Date | undefined) => void
  placeholder?: string
  compact?: boolean
  'aria-label'?: string
}

export function DatePicker(props: DatePickerProps) {
  const { t, i18n } = useTranslation()
  const [open, setOpen] = useState(false)
  const [month, setMonth] = useState(props.selected ?? new Date())
  const select = (date: Date | undefined) => {
    props.onSelect(date)
    setOpen(false)
  }
  return (
    <Popover
      open={open}
      onOpenChange={(next) => {
        if (next) setMonth(props.selected ?? new Date())
        setOpen(next)
      }}
    >
      <PopoverTrigger
        render={
          <Button
            type='button'
            variant='outline'
            aria-label={props['aria-label']}
            data-empty={!props.selected}
            className={
              props.compact
                ? 'size-9 shrink-0 rounded-lg px-2'
                : 'data-[empty=true]:text-muted-foreground h-10 w-[240px] max-w-full justify-start gap-2 rounded-lg text-start font-normal'
            }
          />
        }
      >
        <CalendarDays
          aria-hidden='true'
          className='text-muted-foreground size-4 shrink-0'
        />
        {!props.compact && (
          <>
            <span className='min-w-0 flex-1 truncate font-mono tabular-nums'>
              {dateInputValue(props.selected) ||
                props.placeholder ||
                t('Pick a date')}
            </span>
            <ChevronDown
              aria-hidden='true'
              className='text-muted-foreground size-3.5'
            />
          </>
        )}
      </PopoverTrigger>
      <PopoverContent
        align='start'
        collisionPadding={12}
        className='max-h-[min(600px,var(--available-height,calc(100dvh-1.5rem)))] w-[min(310px,calc(100vw-1.5rem))] gap-0 overflow-hidden rounded-xl p-0'
      >
        <div className='flex min-h-0 justify-center overflow-y-auto p-2'>
          <Calendar
            mode='single'
            required
            month={month}
            onMonthChange={setMonth}
            captionLayout='dropdown'
            selected={props.selected}
            onSelect={select}
            locale={calendarLocale(i18n.language)}
            startMonth={new Date(1900, 0)}
            endMonth={new Date()}
            disabled={(date: Date) =>
              date > new Date() || date < new Date(1900, 0, 1)
            }
            className='[--cell-size:--spacing(9)]'
          />
        </div>
        <div className='bg-muted/20 flex shrink-0 justify-between gap-2 border-t p-2'>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            onClick={() => {
              const today = new Date()
              today.setHours(0, 0, 0, 0)
              select(today)
            }}
          >
            {t('Today')}
          </Button>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            disabled={!props.selected}
            onClick={() => select(undefined)}
          >
            {t('Clear')}
          </Button>
        </div>
      </PopoverContent>
    </Popover>
  )
}
