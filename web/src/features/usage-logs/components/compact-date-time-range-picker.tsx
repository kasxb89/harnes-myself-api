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
import { useMediaQuery } from '@/hooks/use-media-query'
import dayjs from '@/lib/dayjs'
import { cn } from '@/lib/utils'

interface CompactDateTimeRangePickerProps {
  start?: Date
  end?: Date
  onChange: (range: { start?: Date; end?: Date }) => void
  className?: string
}

export function CompactDateTimeRangePicker(
  props: CompactDateTimeRangePickerProps
) {
  const { t, i18n } = useTranslation()
  const id = useId()
  const wide = useMediaQuery('(min-width: 768px)')
  const [open, setOpen] = useState(false)
  const [choosingEnd, setChoosingEnd] = useState(false)
  const [startDate, setStartDate] = useState(dateInputValue(props.start))
  const [endDate, setEndDate] = useState(dateInputValue(props.end))
  const [startTime, setStartTime] = useState(timeInputValue(props.start))
  const [endTime, setEndTime] = useState(timeInputValue(props.end, '23:59:59'))
  const [month, setMonth] = useState(props.start ?? props.end ?? new Date())
  const start = startDate ? parseDateTime(startDate, startTime) : undefined
  const end = endDate ? parseDateTime(endDate, endTime) : undefined
  const invalidStart = Boolean(startDate && !start)
  const invalidEnd = Boolean(endDate && !end)
  const reversed = Boolean(start && end && start > end)
  const invalid = invalidStart || invalidEnd || reversed
  const startDay = parseDateTime(startDate)
  const endDay = parseDateTime(endDate)
  const selected =
    startDay || endDay ? { from: startDay, to: endDay } : undefined
  const label =
    !props.start && !props.end
      ? t('Date Range')
      : `${props.start ? dayjs(props.start).format('YYYY-MM-DD HH:mm:ss') : '-'} ~ ${props.end ? dayjs(props.end).format('YYYY-MM-DD HH:mm:ss') : '-'}`
  let mobileLabel = label
  if (props.start && props.end && dayjs(props.start).isSame(props.end, 'day')) {
    mobileLabel = `${dayjs(props.start).format('MM/DD HH:mm:ss')}–${dayjs(props.end).format('HH:mm:ss')}`
  }
  const onOpenChange = (next: boolean) => {
    if (next) {
      setChoosingEnd(false)
      setStartDate(dateInputValue(props.start))
      setEndDate(dateInputValue(props.end))
      setStartTime(timeInputValue(props.start))
      setEndTime(timeInputValue(props.end, '23:59:59'))
      setMonth(props.start ?? props.end ?? new Date())
    }
    setOpen(next)
  }
  // Calendar-day shortcuts remain inclusive. The week uses the same first
  // weekday as the visible localized calendar.
  // Rolling shortcuts use the exact current instant. Callers retain all their
  // existing lookback, future-time and authorization restrictions.
  const applyPreset = (
    kind: '1h' | '24h' | 'today' | 'yesterday' | '7d' | 'week' | '30d' | 'month'
  ) => {
    const now = dayjs()
    const weekStart = calendarLocale(i18n.language).options?.weekStartsOn ?? 0
    const firstWeekDay = now
      .subtract((now.day() - weekStart + 7) % 7, 'day')
      .startOf('day')
    const presets = {
      '1h': { start: now.subtract(1, 'hour').toDate(), end: now.toDate() },
      '24h': { start: now.subtract(24, 'hour').toDate(), end: now.toDate() },
      today: {
        start: now.startOf('day').toDate(),
        end: now.endOf('day').toDate(),
      },
      yesterday: {
        start: now.subtract(1, 'day').startOf('day').toDate(),
        end: now.subtract(1, 'day').endOf('day').toDate(),
      },
      '7d': {
        start: now.subtract(6, 'day').startOf('day').toDate(),
        end: now.endOf('day').toDate(),
      },
      week: {
        start: firstWeekDay.toDate(),
        end: firstWeekDay.add(6, 'day').endOf('day').toDate(),
      },
      '30d': {
        start: now.subtract(29, 'day').startOf('day').toDate(),
        end: now.endOf('day').toDate(),
      },
      month: {
        start: now.startOf('month').toDate(),
        end: now.endOf('month').toDate(),
      },
    }
    props.onChange(presets[kind])
    setOpen(false)
  }
  const shortcuts = [
    { kind: 'today', label: t('Today') },
    { kind: 'yesterday', label: t('Yesterday') },
    { kind: '1h', label: t('Last hour') },
    { kind: '24h', label: t('Last 24 hours') },
    { kind: '7d', label: t('7 Days') },
    { kind: 'week', label: t('This week') },
    { kind: '30d', label: t('30 Days') },
    { kind: 'month', label: t('Current month') },
  ] as const
  return (
    <Popover open={open} onOpenChange={onOpenChange}>
      <PopoverTrigger
        render={
          <Button
            type='button'
            variant='outline'
            aria-label={label}
            className={cn(
              'h-8 w-full min-w-0 justify-start gap-2 rounded-lg px-2.5 py-1 text-sm font-normal tabular-nums',
              !props.start && !props.end && 'text-muted-foreground',
              props.className
            )}
          />
        }
      >
        <CalendarDays
          aria-hidden='true'
          className='text-muted-foreground size-4 shrink-0'
        />
        <span className='hidden min-w-0 flex-1 truncate text-start sm:block'>
          {label}
        </span>
        <span className='min-w-0 flex-1 truncate text-start sm:hidden'>
          {mobileLabel}
        </span>
        <ChevronDown
          aria-hidden='true'
          className='text-muted-foreground size-3.5 shrink-0'
        />
      </PopoverTrigger>
      <PopoverContent
        align='start'
        collisionPadding={12}
        className='max-h-[min(820px,var(--available-height,calc(100dvh-1.5rem)))] w-[min(760px,calc(100vw-1.5rem))] gap-0 overflow-hidden rounded-xl p-0'
      >
        <div className='flex shrink-0 items-start justify-between gap-3 border-b px-4 py-3'>
          <div>
            <PopoverTitle>{t('Choose date and time range')}</PopoverTitle>
            <p className='text-muted-foreground mt-1 text-xs'>
              {t(
                'Select dates on the calendar, then fine-tune hours, minutes and seconds.'
              )}
            </p>
          </div>
          <Clock3
            aria-hidden='true'
            className='text-muted-foreground mt-0.5 size-4 shrink-0'
          />
        </div>
        <div className='grid min-h-0 overflow-y-auto md:grid-cols-[128px_minmax(0,1fr)]'>
          <div className='bg-muted/20 border-b p-3 md:border-r md:border-b-0'>
            <p className='text-muted-foreground mb-2 text-xs font-medium'>
              {t('Quick select')}
            </p>
            <div className='grid grid-cols-4 gap-1.5 md:grid-cols-1'>
              {shortcuts.map((item) => (
                <Button
                  type='button'
                  variant='ghost'
                  size='sm'
                  key={item.kind}
                  className='h-auto min-h-8 justify-center px-1.5 py-1.5 text-xs whitespace-normal md:justify-start md:px-2'
                  onClick={() => applyPreset(item.kind)}
                >
                  {item.label}
                </Button>
              ))}
            </div>
          </div>
          <div className='min-w-0 px-3 pt-2 pb-3'>
            <div className='flex justify-center'>
              <Calendar
                mode='range'
                selected={selected}
                month={month}
                onMonthChange={setMonth}
                numberOfMonths={wide ? 2 : 1}
                captionLayout='dropdown'
                startMonth={new Date(1900, 0)}
                endMonth={new Date(new Date().getFullYear() + 100, 11)}
                locale={calendarLocale(i18n.language)}
                className='[--cell-size:--spacing(9)]'
                onSelect={(_range, day) => {
                  if (!choosingEnd || !startDay) {
                    setStartDate(dateInputValue(day))
                    setEndDate('')
                    setChoosingEnd(true)
                    return
                  }
                  if (day < startDay) {
                    setStartDate(dateInputValue(day))
                    setEndDate(dateInputValue(startDay))
                  } else {
                    setEndDate(dateInputValue(day))
                  }
                  setChoosingEnd(false)
                }}
              />
            </div>
            {choosingEnd && (
              <p role='status' className='text-primary mt-2 text-xs'>
                {t('Choose an end date to complete the range.')}
              </p>
            )}
            <div className='mt-3 grid grid-cols-2 gap-3 border-t pt-3'>
              <div className='min-w-0 space-y-2'>
                <label
                  htmlFor={`${id}-start`}
                  className='text-muted-foreground text-xs font-medium'
                >
                  {t('Start Time')}
                </label>
                <Input
                  id={`${id}-start`}
                  aria-label={t('Start date')}
                  aria-invalid={invalidStart || reversed}
                  aria-describedby={invalid ? `${id}-error` : undefined}
                  placeholder='YYYY-MM-DD'
                  autoComplete='off'
                  className='h-10 min-w-0 px-2 font-mono text-xs tabular-nums sm:text-sm'
                  value={startDate}
                  onChange={(event) => {
                    setChoosingEnd(false)
                    setStartDate(event.target.value)
                    const day = parseDateTime(event.target.value)
                    if (day) setMonth(day)
                  }}
                />
                <TimeInput
                  label={t('Start Time')}
                  value={startTime}
                  onChange={setStartTime}
                  invalid={invalidStart && Boolean(startDay)}
                />
              </div>
              <div className='min-w-0 space-y-2'>
                <label
                  htmlFor={`${id}-end`}
                  className='text-muted-foreground text-xs font-medium'
                >
                  {t('End Time')}
                </label>
                <Input
                  id={`${id}-end`}
                  aria-label={t('End date')}
                  aria-invalid={invalidEnd || reversed}
                  aria-describedby={invalid ? `${id}-error` : undefined}
                  placeholder='YYYY-MM-DD'
                  autoComplete='off'
                  className='h-10 min-w-0 px-2 font-mono text-xs tabular-nums sm:text-sm'
                  value={endDate}
                  onChange={(event) => {
                    setChoosingEnd(false)
                    setEndDate(event.target.value)
                    const day = parseDateTime(event.target.value)
                    if (day) setMonth(day)
                  }}
                />
                <TimeInput
                  label={t('End Time')}
                  value={endTime}
                  onChange={setEndTime}
                  invalid={invalidEnd && Boolean(endDay)}
                />
              </div>
            </div>
            <p className='text-muted-foreground mt-3 text-xs'>
              {t('Browser time zone: {{zone}}', { zone: browserTimeZone() })}
            </p>
            {invalid && (
              <p
                id={`${id}-error`}
                role='alert'
                className='text-destructive mt-2 text-xs'
              >
                {reversed
                  ? t('Start time must not be later than end time.')
                  : t('Enter a valid date and 24-hour time.')}
              </p>
            )}
          </div>
        </div>
        <div className='bg-muted/20 flex shrink-0 items-center justify-between gap-2 border-t p-3'>
          <Button
            type='button'
            variant='ghost'
            size='sm'
            onClick={() => {
              props.onChange({ start: undefined, end: undefined })
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
              disabled={invalid || choosingEnd}
              onClick={() => {
                if (invalid || choosingEnd) return
                props.onChange({ start, end })
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
