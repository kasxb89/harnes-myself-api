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
import i18next from 'i18next'
import { useCallback, useState } from 'react'

import { handleServerError } from '@/lib/handle-server-error'

import { createWechatNativeOrder, isApiSuccess } from '../api'
import type { WechatNativeOrder } from '../types'

/**
 * Drives the in-console WeChat Native flow: creates the order the dialog shows a
 * code for, and owns the two pieces of state that dialog needs.
 *
 * Settlement is not awaited. The payer scans outside the browser, so the dialog
 * polls the order and tells the caller through its own `onPaid`.
 */
export function useWechatNativePayment() {
  const [processing, setProcessing] = useState(false)
  const [order, setOrder] = useState<WechatNativeOrder | null>(null)
  const [dialogOpen, setDialogOpen] = useState(false)

  const processWechatNativePayment = useCallback(
    async (topupAmount: number) => {
      try {
        setProcessing(true)
        const response = await createWechatNativeOrder(Math.floor(topupAmount))
        if (!isApiSuccess(response) || !response.data?.code_url) {
          handleServerError(response, i18next.t('Payment request failed'))
          return false
        }
        setOrder(response.data)
        setDialogOpen(true)
        return true
      } catch (error) {
        handleServerError(error, i18next.t('Payment request failed'))
        return false
      } finally {
        setProcessing(false)
      }
    },
    []
  )

  return {
    processing,
    order,
    dialogOpen,
    setDialogOpen,
    processWechatNativePayment,
  }
}
