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
import { CheckCircle2 } from 'lucide-react'
import { QRCodeSVG } from 'qrcode.react'
import { useEffect, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { formatLocalCurrencyAmount } from '@/lib/currency'

import { getWechatOrderStatus } from '../../api'
import type { WechatNativeOrder } from '../../types'

interface WechatPayDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  order: WechatNativeOrder | null
  /** Called once the order settles, so the caller can refresh the balance. */
  onPaid: () => void
}

/** How often the settlement state is re-read while the payer scans. */
const POLL_INTERVAL_MS = 3000

/** Seconds left before the order stops being payable, floored at zero. */
function secondsLeft(expiresAt: number, now: number): number {
  return Math.max(0, Math.floor(expiresAt - now / 1000))
}

export function WechatPayDialog(props: WechatPayDialogProps) {
  const { t } = useTranslation()
  const [paid, setPaid] = useState(false)
  const [now, setNow] = useState(() => Date.now())

  const order = props.order
  const remaining = order === null ? 0 : secondsLeft(order.expires_at, now)
  const expired = order !== null && !paid && remaining <= 0

  useEffect(() => {
    if (!props.open || order === null) return
    setPaid(false)
    setNow(Date.now())
  }, [props.open, order])

  // The countdown is local, so it keeps moving between polls.
  useEffect(() => {
    if (!props.open || order === null || paid || expired) return
    const ticker = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(ticker)
  }, [props.open, order, paid, expired])

  useEffect(() => {
    if (!props.open || order === null || paid || expired) return
    let stopped = false
    const poll = setInterval(() => {
      void getWechatOrderStatus(order.out_trade_no)
        .then((response) => {
          if (stopped) return
          const status = response.data?.status
          if (response.success && status === 'success') {
            setPaid(true)
            props.onPaid()
          }
        })
        .catch(() => {
          // A failed poll is retried on the next tick; the payer may still be scanning.
        })
    }, POLL_INTERVAL_MS)
    return () => {
      stopped = true
      clearInterval(poll)
    }
  }, [props.open, order, paid, expired, props])

  let content: ReactNode = null
  if (paid) {
    content = (
      <>
        <CheckCircle2 className='h-12 w-12 text-green-600' />
        <p className='font-medium'>{t('Top-up succeeded')}</p>
        {order !== null && (
          <p className='text-muted-foreground text-sm'>
            {formatLocalCurrencyAmount(order.amount, {
              digitsLarge: 2,
              digitsSmall: 2,
              abbreviate: false,
            })}
          </p>
        )}
      </>
    )
  } else if (expired) {
    content = (
      <Alert>
        <AlertDescription>
          {t('This order has expired. Close this dialog and try again.')}
        </AlertDescription>
      </Alert>
    )
  } else if (order !== null) {
    content = (
      <>
        <div className='rounded-lg bg-white p-3'>
          <QRCodeSVG value={order.code_url} size={220} />
        </div>
        <div className='text-center'>
          <p className='text-lg font-semibold'>
            {formatLocalCurrencyAmount(order.amount, {
              digitsLarge: 2,
              digitsSmall: 2,
              abbreviate: false,
            })}
          </p>
          <p className='text-muted-foreground mt-1 text-sm'>
            {t('Expires in {{seconds}}s', { seconds: remaining })}
          </p>
        </div>
      </>
    )
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      title={t('WeChat Pay')}
      description={t('Scan the QR code with WeChat to complete the top-up')}
      contentClassName='sm:max-w-md'
      contentHeight='auto'
      footer={
        <Button variant='outline' onClick={() => props.onOpenChange(false)}>
          {paid ? t('Close') : t('Cancel')}
        </Button>
      }
    >
      <div className='flex flex-col items-center gap-4 py-2'>{content}</div>
    </Dialog>
  )
}
