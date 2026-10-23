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
import {
  Activity,
  CircleDollarSign,
  PiggyBank,
  Receipt,
  TrendingUp,
  UserPlus,
  Users,
  Wallet,
} from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
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
import {
  StatCard,
  type StatCardDetail,
} from '@/features/dashboard/components/ui/stat-card'
import { hasPermission } from '@/lib/admin-permissions'
import { api } from '@/lib/api'
import {
  formatLocalCurrencyAmount,
  formatQuotaWithCurrency,
  type CurrencyFormatOptions,
} from '@/lib/currency'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

import {
  ConsumptionTrendChart,
  type DailyOperationsPoint,
} from './components/consumption-trend-chart'
import { DailyBarChart } from './components/daily-bar-chart'

/** One hand-entered payment this deployment made to an upstream provider. */
type UpstreamTopup = {
  id: number
  upstream: string
  amount_cents: number
  currency: string
  paid_at: number
  note: string
}

type UserGrowth = {
  /** Registered accounts, soft-deleted excluded. */
  total: number
  /** Accounts created since local midnight. */
  new_today: number
  /** Remaining quota across all accounts: prepaid money not yet spent. */
  balance_quota: number
}

type TopUpStats = {
  money_total: number
  count_total: number
  users_total: number
  money_today: number
  count_today: number
  users_today: number
}

/** One account's completed top-up history. */
type UserTopUpTotal = {
  user_id: number
  username: string
  money: number
  count: number
  last_paid_at: number
}

/** One completed payment, newest first. */
type TopUpRecord = {
  id: number
  user_id: number
  username: string
  money: number
  provider: string
  completed_at: number
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
  /** Requests served since local midnight. */
  calls_today: number
  users: UserGrowth
  topups: TopUpStats
  /** Completed top-ups summed per account, largest payer first. */
  topup_users: UserTopUpTotal[]
  /** The latest completed payments, newest first. */
  recent_topups: TopUpRecord[]
  /** Per-day operations, oldest first, for the trend charts. */
  daily: DailyOperationsPoint[]
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

function formatDateTime(seconds: number): string {
  return new Date(seconds * 1000).toLocaleString()
}

/** The date input's value for a Unix timestamp, in the local calendar. */
function dayValue(seconds: number): string {
  const date = new Date(seconds * 1000)
  const month = `${date.getMonth() + 1}`.padStart(2, '0')
  const day = `${date.getDate()}`.padStart(2, '0')
  return `${date.getFullYear()}-${month}-${day}`
}

/** Cards share one wrapper so a metric row reads as a single surface. */
function MetricRow(props: { children: ReactNode }) {
  return (
    <div className='bg-background/60 grid grid-cols-2 gap-2.5 rounded-xl border p-3 sm:gap-4 sm:p-4 xl:grid-cols-4'>
      <div className='contents'>{props.children}</div>
    </div>
  )
}

function SectionHeader(props: { title: string; description: string }) {
  return (
    <div className='flex flex-col gap-1'>
      <h3 className='text-sm font-semibold sm:text-base'>{props.title}</h3>
      <p className='text-muted-foreground text-xs sm:text-sm'>
        {props.description}
      </p>
    </div>
  )
}

export function UpstreamTopupsPage() {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const user = useAuthStore((state) => state.auth.user)
  const isAdmin = (user?.role ?? 0) >= ROLE.ADMIN
  const canReadOverview = isAdmin && hasPermission(user, 'operations', 'read')
  const canReadLedger =
    isAdmin && hasPermission(user, 'upstream_topups', 'read')
  const canWriteLedger =
    isAdmin && hasPermission(user, 'upstream_topups', 'write')
  const [form, setForm] = useState(EMPTY_FORM)
  const [open, setOpen] = useState(false)

  const overview = useQuery({
    queryKey: ['operations-overview', user?.id],
    enabled: canReadOverview,
    queryFn: async (): Promise<Omit<LedgerPayload, 'items'>> => {
      const response = await api.get('/api/operations/overview')
      return requireServerSuccess(response.data).data
    },
  })
  const ledger = useQuery({
    queryKey: ['upstream-topups', user?.id],
    enabled: canReadLedger,
    queryFn: async (): Promise<
      Pick<LedgerPayload, 'items' | 'spent_cents'>
    > => {
      const response = await api.get('/api/upstream-topup/')
      return requireServerSuccess(response.data).data
    },
  })

  const invalidate = () =>
    Promise.all([
      queryClient.invalidateQueries({ queryKey: ['upstream-topups'] }),
      queryClient.invalidateQueries({ queryKey: ['operations-overview'] }),
    ])
  const onError = (error: unknown) => {
    handleServerError(error)
  }

  const save = useMutation({
    mutationFn: async () => {
      if (!canWriteLedger) {
        throw new Error(
          t('You do not have permission to edit upstream top-ups.')
        )
      }
      const body = {
        upstream: form.upstream.trim(),
        amount_cents: Math.round(Number(form.amount) * 100),
        currency: form.currency.trim() || 'CNY',
        paid_at: form.paidAt
          ? Math.floor(new Date(`${form.paidAt}T00:00:00`).getTime() / 1000)
          : 0,
        note: form.note,
      }
      const response =
        form.id > 0
          ? await api.put(`/api/upstream-topup/${form.id}`, body)
          : await api.post('/api/upstream-topup/', body)
      requireServerSuccess(response.data)
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
      if (!canWriteLedger) {
        throw new Error(
          t('You do not have permission to edit upstream top-ups.')
        )
      }
      requireServerSuccess((await api.delete(`/api/upstream-topup/${id}`)).data)
    },
    onSuccess: async () => {
      toast.success(t('Deleted'))
      await invalidate()
    },
    onError,
  })

  const overviewData = canReadOverview ? overview.data : undefined
  const ledgerData = canReadLedger ? ledger.data : undefined
  const items = ledgerData?.items ?? []
  const spentCny = (overviewData?.spent_cents?.CNY ?? 0) / 100
  const received = Object.values(overviewData?.received ?? {}).reduce(
    (sum, value) => sum + value,
    0
  )
  const consumedToday = formatQuotaWithCurrency(
    overviewData?.consumed_quota_today ?? 0,
    MONEY_OPTIONS
  )
  const consumedTotal = formatQuotaWithCurrency(
    overviewData?.consumed_quota ?? 0,
    MONEY_OPTIONS
  )
  const balance = formatQuotaWithCurrency(
    overviewData?.users?.balance_quota ?? 0,
    MONEY_OPTIONS
  )
  const profit = received - spentCny
  const daily = overviewData?.daily ?? []
  const users = overviewData?.users
  const topups = overviewData?.topups
  const calls = daily.map((point) => point.calls)
  const newUsersDaily = daily.map((point) => ({
    day_start: point.day_start,
    value: point.new_users,
  }))
  const topUpDaily = daily.map((point) => ({
    day_start: point.day_start,
    value: point.topup_money,
  }))
  const callsInWindow = calls.reduce((sum, value) => sum + value, 0)
  const payingRate =
    users && users.total > 0 && topups
      ? `${((topups.users_total / users.total) * 100).toFixed(0)}%`
      : '—'
  const otherCurrencies = Object.entries(ledgerData?.spent_cents ?? {}).filter(
    ([currency]) => currency !== 'CNY'
  )

  const detail = (label: string, value: string): StatCardDetail => ({
    label,
    value,
  })

  return (
    <div className='flex flex-col gap-5'>
      {canReadOverview && overview.isError && (
        <ErrorState
          title={t('Unable to load business overview')}
          onRetry={() => {
            void overview.refetch()
          }}
        />
      )}
      {canReadOverview && !overview.isError && (
        <div className='contents'>
          <section className='bg-card overflow-hidden rounded-2xl border p-4 shadow-xs sm:p-5'>
            <SectionHeader
              title={t('Today')}
              description={t('Live figures since local midnight')}
            />
            <div className='mt-4'>
              <MetricRow>
                <StatCard
                  title={t('User consumption')}
                  value={consumedToday}
                  description={`${t('Total')} ${consumedTotal}`}
                  icon={Wallet}
                  tone='accent-1'
                  sparklineVariant='line'
                  sparkline={daily.map((point) => point.quota)}
                  loading={overview.isLoading}
                  compactMobile
                />
                <StatCard
                  title={t('Today top-ups')}
                  value={formatLocalCurrencyAmount(topups?.money_today ?? 0)}
                  description={`${t('Top-up count')} ${topups?.count_today ?? 0} · ${t('Top-up users')} ${topups?.users_today ?? 0}`}
                  icon={CircleDollarSign}
                  tone='accent-2'
                  sparkline={topUpDaily.map((point) => point.value)}
                  loading={overview.isLoading}
                  compactMobile
                />
                <StatCard
                  title={t('New users today')}
                  value={users?.new_today ?? 0}
                  description={`${t('Total')} ${users?.total ?? 0}`}
                  icon={UserPlus}
                  tone='accent-3'
                  sparkline={newUsersDaily.map((point) => point.value)}
                  loading={overview.isLoading}
                  compactMobile
                />
                <StatCard
                  title={t('Calls today')}
                  value={overviewData?.calls_today ?? 0}
                  description={`${t('Active users today')} ${overviewData?.active_users_today ?? 0}`}
                  icon={Activity}
                  tone='accent-1'
                  sparkline={calls}
                  loading={overview.isLoading}
                  compactMobile
                />
              </MetricRow>
            </div>
          </section>

          <section className='bg-card overflow-hidden rounded-2xl border p-4 shadow-xs sm:p-5'>
            <SectionHeader
              title={t('Consumption trend')}
              description={t('Last 14 days')}
            />
            <div className='mt-4 grid gap-4 lg:grid-cols-3'>
              <div className='flex flex-col gap-2'>
                <span className='text-muted-foreground text-xs font-medium'>
                  {t('Daily consumption')}
                </span>
                <div className='h-[220px]'>
                  <ConsumptionTrendChart points={daily} />
                </div>
              </div>
              <div className='flex flex-col gap-2'>
                <span className='text-muted-foreground text-xs font-medium'>
                  {t('Daily top-ups')}
                </span>
                <div className='h-[220px]'>
                  <DailyBarChart
                    points={topUpDaily}
                    tickFormat={(value) =>
                      value >= 100 ? value.toFixed(0) : value.toFixed(2)
                    }
                  />
                </div>
              </div>
              <div className='flex flex-col gap-2'>
                <span className='text-muted-foreground text-xs font-medium'>
                  {t('Daily new users')}
                </span>
                <div className='h-[220px]'>
                  <DailyBarChart
                    points={newUsersDaily}
                    tickFormat={(value) => value.toFixed(0)}
                    minInterval={1}
                  />
                </div>
              </div>
            </div>
          </section>

          <section className='bg-card overflow-hidden rounded-2xl border p-4 shadow-xs sm:p-5'>
            <SectionHeader
              title={t('All time')}
              description={t('Money, accounts, and margin')}
            />
            <div className='mt-4'>
              <MetricRow>
                <StatCard
                  title={t('Users paid in')}
                  value={formatLocalCurrencyAmount(received)}
                  description={t('Completed top-ups')}
                  icon={Receipt}
                  tone='accent-2'
                  details={[
                    detail(t('Top-up count'), String(topups?.count_total ?? 0)),
                    detail(t('Top-up users'), String(topups?.users_total ?? 0)),
                  ]}
                  loading={overview.isLoading}
                  compactMobile
                />
                <StatCard
                  title={t('User consumption')}
                  value={consumedTotal}
                  description={t('Recognised once users spend it')}
                  icon={TrendingUp}
                  tone='accent-1'
                  details={[
                    detail(t('Today'), consumedToday),
                    detail(t('Calls in 14 days'), String(callsInWindow)),
                  ]}
                  loading={overview.isLoading}
                  compactMobile
                />
                <StatCard
                  title={t('Users')}
                  value={users?.total ?? 0}
                  description={t('Registered accounts')}
                  icon={Users}
                  tone='accent-3'
                  details={[
                    detail(t('New today'), String(users?.new_today ?? 0)),
                    detail(t('Unused balance'), balance),
                  ]}
                  loading={overview.isLoading}
                  compactMobile
                />
                <StatCard
                  title={t('Profit')}
                  value={formatLocalCurrencyAmount(profit)}
                  description={t('Received minus paid upstream')}
                  icon={PiggyBank}
                  tone={profit >= 0 ? 'accent-3' : 'accent-2'}
                  details={[
                    detail(
                      t('Paid upstream'),
                      formatLocalCurrencyAmount(spentCny)
                    ),
                    detail(
                      t('Paying users'),
                      `${topups?.users_total ?? 0} · ${payingRate}`
                    ),
                  ]}
                  loading={overview.isLoading}
                  compactMobile
                />
              </MetricRow>
            </div>
          </section>

          <section className='bg-card overflow-hidden rounded-2xl border p-4 shadow-xs sm:p-5'>
            <SectionHeader
              title={t('User top-ups')}
              description={t(
                'Completed payments only. Amount is what the user paid.'
              )}
            />
            <div className='mt-4 grid gap-5 xl:grid-cols-2'>
              <div className='flex flex-col gap-2'>
                <span className='text-muted-foreground text-xs font-medium'>
                  {t('Per user')}
                </span>
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>{t('User')}</TableHead>
                      <TableHead className='text-right'>
                        {t('Total paid')}
                      </TableHead>
                      <TableHead className='text-right'>
                        {t('Top-up count')}
                      </TableHead>
                      <TableHead>{t('Last top-up')}</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {(overviewData?.topup_users ?? []).map((row) => (
                      <TableRow key={row.user_id}>
                        <TableCell>
                          {row.username || `#${row.user_id}`}
                        </TableCell>
                        <TableCell className='text-right tabular-nums'>
                          {formatLocalCurrencyAmount(row.money)}
                        </TableCell>
                        <TableCell className='text-right tabular-nums'>
                          {row.count}
                        </TableCell>
                        <TableCell>{formatDay(row.last_paid_at)}</TableCell>
                      </TableRow>
                    ))}
                    {(overviewData?.topup_users ?? []).length === 0 && (
                      <TableRow>
                        <TableCell
                          colSpan={4}
                          className='text-muted-foreground text-center text-sm'
                        >
                          {t('No completed top-ups yet')}
                        </TableCell>
                      </TableRow>
                    )}
                  </TableBody>
                </Table>
              </div>

              <div className='flex flex-col gap-2'>
                <span className='text-muted-foreground text-xs font-medium'>
                  {t('Recent top-ups')}
                </span>
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>{t('Time')}</TableHead>
                      <TableHead>{t('User')}</TableHead>
                      <TableHead className='text-right'>
                        {t('Amount')}
                      </TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    {(overviewData?.recent_topups ?? []).map((row) => (
                      <TableRow key={row.id}>
                        <TableCell>
                          {formatDateTime(row.completed_at)}
                        </TableCell>
                        <TableCell>
                          {row.username || `#${row.user_id}`}
                        </TableCell>
                        <TableCell className='text-right tabular-nums'>
                          {formatLocalCurrencyAmount(row.money)}
                        </TableCell>
                      </TableRow>
                    ))}
                    {(overviewData?.recent_topups ?? []).length === 0 && (
                      <TableRow>
                        <TableCell
                          colSpan={3}
                          className='text-muted-foreground text-center text-sm'
                        >
                          {t('No completed top-ups yet')}
                        </TableCell>
                      </TableRow>
                    )}
                  </TableBody>
                </Table>
              </div>
            </div>
          </section>
        </div>
      )}

      {canReadLedger && (
        <Card>
          <CardHeader className='flex flex-row items-center justify-between gap-3'>
            <CardTitle>{t('Upstream top-ups')}</CardTitle>
            <Button
              disabled={!canWriteLedger}
              onClick={() => {
                setForm(EMPTY_FORM)
                setOpen(true)
              }}
            >
              {t('Record a top-up')}
            </Button>
          </CardHeader>
          <CardContent>
            {ledger.isError && (
              <ErrorState
                title={t('Unable to load upstream top-ups')}
                onRetry={() => {
                  void ledger.refetch()
                }}
              />
            )}
            {ledger.isPending && <LoadingState />}
            {!ledger.isError && !ledger.isPending && (
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
                          disabled={
                            !canWriteLedger ||
                            save.isPending ||
                            remove.isPending
                          }
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
                          disabled={
                            !canWriteLedger ||
                            save.isPending ||
                            remove.isPending
                          }
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
            )}

            {otherCurrencies.length > 0 && (
              <p className='text-muted-foreground mt-3 text-sm'>
                {t('Paid in other currencies')}:{' '}
                {otherCurrencies
                  .map(([currency, cents]) => formatCents(cents, currency))
                  .join(' · ')}
              </p>
            )}
          </CardContent>
        </Card>
      )}

      <Dialog open={open && canWriteLedger} onOpenChange={setOpen}>
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
