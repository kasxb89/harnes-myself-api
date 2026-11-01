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
import { Clock3 } from 'lucide-react'
import { useRef } from 'react'
import { useTranslation } from 'react-i18next'

import { Input } from '@/components/ui/input'
import { cn } from '@/lib/utils'

// One shared 24-hour editor for single timestamps and range endpoints. Drafts
// remain strings; callers emit a Date only after strict validation succeeds.
export function TimeInput(props: {
  value: string
  onChange: (value: string) => void
  label: string
  invalid?: boolean
  disabled?: boolean
  className?: string
}) {
  const { t } = useTranslation()
  const inputs = useRef<(HTMLInputElement | null)[]>([])
  const advancing = useRef(false)
  const parts = props.value.split(':')
  const labels = [t('Hours'), t('Minutes'), t('Seconds')]
  const update = (index: number, value: string) => {
    const next = [parts[0] ?? '', parts[1] ?? '', parts[2] ?? '']
    next[index] = value
    props.onChange(next.join(':'))
  }
  const focusNext = (index: number) => {
    // Focus changes synchronously, before React commits the two-digit value.
    // Its blur must not pad and overwrite the previous one-digit DOM value.
    advancing.current = true
    inputs.current[index + 1]?.focus()
    advancing.current = false
  }
  return (
    <div
      role='group'
      aria-label={props.label}
      aria-invalid={props.invalid || undefined}
      className={cn(
        'border-input bg-background flex h-10 min-w-0 items-center gap-0.5 rounded-lg border px-2 shadow-xs focus-within:border-ring focus-within:ring-ring/25 focus-within:ring-2',
        props.invalid && 'border-destructive',
        props.disabled && 'opacity-50',
        props.className
      )}
    >
      <Clock3
        aria-hidden='true'
        className='text-muted-foreground mr-1 size-4 shrink-0'
      />
      {labels.map((label, index) => (
        <div className='contents' key={label}>
          {index > 0 && (
            <span aria-hidden='true' className='text-muted-foreground'>
              :
            </span>
          )}
          <Input
            ref={(node) => {
              inputs.current[index] = node
            }}
            aria-label={`${props.label} · ${label}`}
            aria-invalid={props.invalid || undefined}
            disabled={props.disabled}
            inputMode='numeric'
            autoComplete='off'
            maxLength={2}
            placeholder='00'
            value={parts[index] ?? ''}
            className='h-8 min-w-0 flex-1 rounded-sm border-0 bg-transparent px-0 text-center font-mono text-sm tabular-nums shadow-none focus-visible:ring-0'
            onFocus={(event) => event.currentTarget.select()}
            onClick={(event) => event.currentTarget.select()}
            onChange={(event) => {
              const value = event.target.value
              if (!/^\d{0,2}$/.test(value)) return
              update(index, value)
              if (
                value.length === 2 &&
                Number(value) <= (index === 0 ? 23 : 59)
              ) {
                focusNext(index)
              }
            }}
            onBlur={(event) => {
              if (advancing.current) return
              const value = event.currentTarget.value
              if (value.length === 1) update(index, value.padStart(2, '0'))
            }}
            onKeyDown={(event) => {
              if (
                /^\d$/.test(event.key) &&
                !event.ctrlKey &&
                !event.metaKey &&
                !event.altKey
              ) {
                event.preventDefault()
                const current = parts[index] ?? ''
                const next =
                  current.length === 1 ? current + event.key : event.key
                update(index, next)
                if (
                  next.length === 2 &&
                  Number(next) <= (index === 0 ? 23 : 59)
                ) {
                  focusNext(index)
                }
                return
              }
              if (event.key !== 'ArrowUp' && event.key !== 'ArrowDown') return
              event.preventDefault()
              const limit = index === 0 ? 24 : 60
              const current = Number(parts[index]) || 0
              update(
                index,
                String(
                  (current + (event.key === 'ArrowUp' ? 1 : -1) + limit) % limit
                ).padStart(2, '0')
              )
            }}
            onPaste={(event) => {
              const value = event.clipboardData.getData('text').trim()
              if (!value.includes(':')) return
              event.preventDefault()
              if (!/^([01]\d|2[0-3]):[0-5]\d(:[0-5]\d)?$/.test(value)) return
              props.onChange(value.length === 5 ? `${value}:00` : value)
            }}
          />
        </div>
      ))}
    </div>
  )
}
