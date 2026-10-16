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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { api } from '@/lib/api'
import {
  formatLocalCurrencyAmount,
  formatQuotaWithCurrency,
  type CurrencyFormatOptions,
} from '@/lib/currency'
import { handleServerError } from '@/lib/handle-server-error'

import {
  ConsumptionTrendChart,
  type DailyConsumptionPoint,
} from './components/consumption-trend-chart'

/** One hand-entered payment this deployment made to an upstream provider. */
type UpstreamTopup = {
  id: number
  upstream: string
  amount_cents: number
  currency: string
  paid_at: number
  note: string
}

type LedgerPayload = {
  items: UpstreamTopup[]
  /** Paid upstream, per currency, in the currency's minor unit. */
  spent_cents: Record<string, number>
  /** Received from users, in the deployment's own currency. */
  received: Record<string, number>
  /** Quota users actually consumed in total. Quota, not money: the deployment's
   * own rate converts it, so a re-priced deployment still reads correctly. */
  consumed_quota: number
  /** Quota users consumed since local midnight. */
  consumed_quota_today: number
  /** Users who consumed anything since local midnight. */
  active_users_today: number
  /** Consumption per day, oldest first, for the trend chart. */
  daily: DailyConsumptionPoint[]
}

const EMPTY_FORM = {
  id: 0,
  upstream: '',
  amount: '',
  currency: 'CNY',
  paidAt: '',
  note: '',
}

/** Consumption is reconciled against real payments, so it keeps cents. */
const MONEY_OPTIONS: CurrencyFormatOptions = {
  digitsLarge: 2,
  digitsSmall: 4,
  abbreviate: false,
}

function formatCents(cents: number, currency: string): string {
  return `${(cents / 100).toFixed(2)} ${currency}`
}

function formatDay(seconds: number): string {
  return new Date(seconds * 1000).toLocaleDateString()
}

/** The date input's value for a Unix timestamp, in the local calendar. */
function dayValue(seconds: number): string {
  const date = new Date(seconds * 1000)
  const month = `${date.getMonth() + 1}`.padStart(2, '0')
  const day = `${date.getDate()}`.padStart(2, '0')
  return `${date.getFullYear()}-${month}-${day}`
}

export function UpstreamTopupsPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [form, setForm] = useState(EMPTY_FORM)
  const [open, setOpen] = useState(false)

  const ledger = useQuery({
    queryKey: ['upstream-topups'],
    queryFn: async (): Promise<LedgerPayload> => {
      const response = await api.get('/api/upstream-topup/')
      return response.data.data as LedgerPayload
    },
  })

  const invalidate = () =>
    queryClient.invalidateQueries({ queryKey: ['upstream-topups'] })
  const onError = (error: unknown) => {
    handleServerError(error)
  }

  const save = useMutation({
    mutationFn: async () => {
      const body = {
        upstream: form.upstream.trim(),
        amount_cents: Math.round(Number(form.amount) * 100),
        currency: form.currency.trim() || 'CNY',
        paid_at: form.paidAt
          ? Math.floor(new Date(`${form.paidAt}T00:00:00`).getTime() / 1000)
          : 0,
        note: form.note,
      }
      if (form.id > 0) await api.put(`/api/upstream-topup/${form.id}`, body)
      else await api.post('/api/upstream-topup/', body)
    },
    onSuccess: async () => {
      toast.success(t('Saved'))
      setOpen(false)
      await invalidate()
    },
    onError,
  })

  const remove = useMutation({
    mutationFn: async (id: number) => {
      await api.delete(`/api/upstream-topup/${id}`)
    },
    onSuccess: async () => {
      toast.success(t('Deleted'))
      await invalidate()
    },
    onError,
  })

  const items = ledger.data?.items ?? []
  const spentCny = (ledger.data?.spent_cents?.CNY ?? 0) / 100
  const received = Object.values(ledger.data?.received ?? {}).reduce(
    (sum, value) => sum + value,
    0
  )
  const consumedToday = formatQuotaWithCurrency(
    ledger.data?.consumed_quota_today ?? 0,
    MONEY_OPTIONS
  )
  const consumedTotal = formatQuotaWithCurrency(
    ledger.data?.consumed_quota ?? 0,
    MONEY_OPTIONS
  )
  const profit = received - spentCny
  const daily = ledger.data?.daily ?? []
  // The series is dense and ends today, so its last point is today's row.
  const callsToday = daily.at(-1)?.calls ?? 0
  const otherCurrencies = Object.entries(ledger.data?.spent_cents ?? {}).filter(
    ([currency]) => currency !== 'CNY'
  )

  return (
    <div className='flex flex-col gap-4 p-4'>
      <div className='grid gap-4 sm:grid-cols-2 lg:grid-cols-4'>
        <Card>
          <CardHeader>
            <CardTitle>{t('Users paid in')}</CardTitle>
          </CardHeader>
          <CardContent className='text-2xl font-semibold'>
            {formatLocalCurrencyAmount(received)}
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>{t('User consumption')}</CardTitle>
          </CardHeader>
          <CardContent>
            <div className='flex items-baseline gap-2'>
              <span className='text-muted-foreground text-sm'>
                {t('Today')}
              </span>
              <span className='text-2xl font-semibold'>{consumedToday}</span>
            </div>
            <p className='text-muted-foreground text-sm'>
              {t('Total')} {consumedTotal}
            </p>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>{t('Paid upstream')}</CardTitle>
          </CardHeader>
          <CardContent className='text-2xl font-semibold'>
            {formatLocalCurrencyAmount(spentCny)}
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>{t('Profit')}</CardTitle>
          </CardHeader>
          <CardContent
            className={
              profit >= 0
                ? 'text-2xl font-semibold text-emerald-600'
                : 'text-2xl font-semibold text-red-600'
            }
          >
            {formatLocalCurrencyAmount(profit)}
          </CardContent>
        </Card>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>{t('Consumption trend')}</CardTitle>
        </CardHeader>
        <CardContent>
          <p className='text-muted-foreground mb-3 text-sm'>
            {t('Active users today')} {ledger.data?.active_users_today ?? 0} ·{' '}
            {t('Calls today')} {callsToday}
          </p>
          <div className='h-[280px]'>
            <ConsumptionTrendChart points={daily} />
          </div>
        </CardContent>
      </Card>

      <div className='flex items-center justify-between'>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Record what you actually paid each upstream by hand. Profit is what users paid in minus what this deployment paid upstream - prepaid balance counts as revenue, so read it next to user consumption. Top-ups entered in another currency are listed separately below.'
          )}
        </p>
        <Button
          onClick={() => {
            setForm(EMPTY_FORM)
            setOpen(true)
          }}
        >
          {t('Record a top-up')}
        </Button>
      </div>

      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('Upstream')}</TableHead>
            <TableHead>{t('Amount')}</TableHead>
            <TableHead>{t('Paid on')}</TableHead>
            <TableHead>{t('Note')}</TableHead>
            <TableHead className='text-right'>{t('Actions')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {items.map((item) => (
            <TableRow key={item.id}>
              <TableCell>{item.upstream}</TableCell>
              <TableCell>
                {formatCents(item.amount_cents, item.currency)}
              </TableCell>
              <TableCell>{formatDay(item.paid_at)}</TableCell>
              <TableCell>{item.note}</TableCell>
              <TableCell className='text-right'>
                <Button
                  variant='ghost'
                  size='sm'
                  onClick={() => {
                    setForm({
                      id: item.id,
                      upstream: item.upstream,
                      amount: (item.amount_cents / 100).toFixed(2),
                      currency: item.currency,
                      paidAt: dayValue(item.paid_at),
                      note: item.note,
                    })
                    setOpen(true)
                  }}
                >
                  {t('Edit')}
                </Button>
                <Button
                  variant='ghost'
                  size='sm'
                  onClick={() => {
                    remove.mutate(item.id)
                  }}
                >
                  {t('Delete')}
                </Button>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>

      {otherCurrencies.length > 0 && (
        <p className='text-muted-foreground text-sm'>
          {t('Paid in other currencies')}:{' '}
          {otherCurrencies
            .map(([currency, cents]) => formatCents(cents, currency))
            .join(' · ')}
        </p>
      )}

      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>
              {form.id > 0 ? t('Edit a top-up') : t('Record a top-up')}
            </DialogTitle>
          </DialogHeader>
          <Input
            placeholder={t('Upstream')}
            value={form.upstream}
            onChange={(event) => {
              setForm({ ...form, upstream: event.target.value })
            }}
          />
          <Input
            inputMode='decimal'
            placeholder={t('Amount')}
            value={form.amount}
            onChange={(event) => {
              setForm({
                ...form,
                amount: event.target.value.replaceAll(/[^\d.]/g, ''),
              })
            }}
          />
          <Input
            placeholder={t('Currency')}
            value={form.currency}
            onChange={(event) => {
              setForm({ ...form, currency: event.target.value })
            }}
          />
          <Input
            type='date'
            value={form.paidAt}
            onChange={(event) => {
              setForm({ ...form, paidAt: event.target.value })
            }}
          />
          <Input
            placeholder={t('Note')}
            value={form.note}
            onChange={(event) => {
              setForm({ ...form, note: event.target.value })
            }}
          />
          <DialogFooter>
            <Button
              variant='outline'
              onClick={() => {
                setOpen(false)
              }}
            >
              {t('Cancel')}
            </Button>
            <Button
              onClick={() => {
                save.mutate()
              }}
              disabled={save.isPending || form.upstream.trim() === ''}
            >
              {t('Save')}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  )
}
