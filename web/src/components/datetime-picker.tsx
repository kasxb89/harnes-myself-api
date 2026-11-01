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
import { CalendarDays, ChevronDown, Clock3 } from 'lucide-react'
import { useId, useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  browserTimeZone,
  calendarLocale,
  dateInputValue,
  parseDateTime,
  timeInputValue,
} from '@/components/date-time-utils'
import { TimeInput } from '@/components/time-input'
import { Button } from '@/components/ui/button'
import { Calendar } from '@/components/ui/calendar'
import { Input } from '@/components/ui/input'
import {
  Popover,
  PopoverContent,
  PopoverTitle,
  PopoverTrigger,
} from '@/components/ui/popover'
import { cn } from '@/lib/utils'

interface DateTimePickerProps {
  value?: Date
  onChange?: (date: Date | undefined) => void
  placeholder?: string
  className?: string
}

export function DateTimePicker(props: DateTimePickerProps) {
  const { t, i18n } = useTranslation()
  const dateId = useId()
  const errorId = useId()
  const [open, setOpen] = useState(false)
  const [date, setDate] = useState(dateInputValue(props.value))
  const [time, setTime] = useState(timeInputValue(props.value))
  const [month, setMonth] = useState(props.value ?? new Date())
  const parsed = parseDateTime(date, time)
  const invalid = Boolean(date && !parsed)
  const selected = parseDateTime(date)
  const currentYear = new Date().getFullYear()
  const displayDate = dateInputValue(props.value)
  const onOpenChange = (next: boolean) => {
    if (next) {
      setDate(dateInputValue(props.value))
      setTime(timeInputValue(props.value))
      setMonth(props.value && displayDate ? props.value : new Date())
    }
    setOpen(next)
  }
  return (
    <Popover open={open} onOpenChange={onOpenChange}>
      <PopoverTrigger
        render={
          <Button
            type='button'
            variant='outline'
            className={cn(
              'h-10 w-full min-w-0 justify-start gap-2 rounded-lg font-normal',
              !displayDate && 'text-muted-foreground',
              props.className
            )}
          />
        }
      >
        <CalendarDays
          aria-hidden='true'
          className='text-muted-foreground size-4 shrink-0'
        />
        <span className='min-w-0 flex-1 truncate text-start font-mono text-sm tabular-nums'>
          {displayDate
            ? `${displayDate} ${timeInputValue(props.value)}`
            : (props.placeholder ?? t('Select date'))}
        </span>
        <ChevronDown
          aria-hidden='true'
          className='text-muted-foreground size-3.5 shrink-0'
        />
      </PopoverTrigger>
      <PopoverContent
        align='start'
        collisionPadding={12}
        className='max-h-[min(720px,var(--available-height,calc(100dvh-1.5rem)))] w-[min(340px,calc(100vw-1.5rem))] gap-0 overflow-hidden rounded-xl p-0'
      >
        <div className='shrink-0 border-b px-4 py-3'>
          <PopoverTitle>{t('Date and time')}</PopoverTitle>
          <p className='text-muted-foreground mt-1 text-xs'>
            {t('Browser time zone: {{zone}}', { zone: browserTimeZone() })}
          </p>
        </div>
        <div className='min-h-0 overflow-y-auto'>
          <div className='flex justify-center px-2 pt-2'>
            <Calendar
              mode='single'
              required
              selected={selected}
              month={month}
              onMonthChange={setMonth}
              captionLayout='dropdown'
              onSelect={(value) => {
                setDate(dateInputValue(value))
                setMonth(value)
              }}
              locale={calendarLocale(i18n.language)}
              startMonth={new Date(currentYear - 100, 0)}
              endMonth={new Date(currentYear + 100, 11)}
              className='[--cell-size:--spacing(9)]'
            />
          </div>
          <div className='space-y-3 px-4 pt-3 pb-4'>
            <div className='space-y-1.5'>
              <label
                htmlFor={dateId}
                className='text-muted-foreground text-xs font-medium'
              >
                {t('Date')}
              </label>
              <Input
                id={dateId}
                value={date}
                placeholder='YYYY-MM-DD'
                autoComplete='off'
                aria-invalid={Boolean(date && !selected)}
                aria-describedby={invalid ? errorId : undefined}
                className='h-10 font-mono tabular-nums'
                onChange={(event) => {
                  setDate(event.target.value)
                  const next = parseDateTime(event.target.value)
                  if (next) setMonth(next)
                }}
              />
            </div>
            <div className='space-y-1.5'>
              <span className='text-muted-foreground text-xs font-medium'>
                {t('Time')}
              </span>
              <TimeInput
                label={t('Time')}
                value={time}
                onChange={setTime}
                invalid={invalid && Boolean(selected)}
              />
            </div>
            {invalid && (
              <p id={errorId} role='alert' className='text-destructive text-xs'>
                {t('Enter a valid date and 24-hour time.')}
              </p>
            )}
            <Button
              type='button'
              variant='secondary'
              size='sm'
              className='w-full'
              onClick={() => {
                const now = new Date()
                setDate(dateInputValue(now))
                setTime(timeInputValue(now))
                setMonth(now)
              }}
            >
              <Clock3 aria-hidden='true' className='size-3.5' />
              {t('Use current date and time')}
            </Button>
          </div>
        </div>
        <div className='bg-muted/20 flex shrink-0 items-center justify-between gap-2 border-t p-3'>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            onClick={() => {
              props.onChange?.(undefined)
              setOpen(false)
            }}
          >
            {t('Clear')}
          </Button>
          <div className='flex gap-2'>
            <Button
              type='button'
              variant='outline'
              size='sm'
              onClick={() => setOpen(false)}
            >
              {t('Cancel')}
            </Button>
            <Button
              type='button'
              size='sm'
              disabled={!parsed}
              onClick={() => {
                if (!parsed) return
                props.onChange?.(parsed)
                setOpen(false)
              }}
            >
              {t('Confirm')}
            </Button>
          </div>
        </div>
      </PopoverContent>
    </Popover>
  )
}
