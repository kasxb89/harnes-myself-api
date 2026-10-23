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
import { z } from 'zod'

import { getMediaOperations } from '@/features/media-operations/api'
import { getMediaConcurrency } from '@/features/media-operations/concurrency-api'
import { getModelProfits } from '@/features/model-profits/api'
import { usageLogSchema } from '@/features/usage-logs/data/schema'
import {
  getActivitySessions,
  getActivitySummary,
} from '@/features/user-activity/api'
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import {
  supportsOperationsModelFilter,
  validOperationsFilters,
  type OperationsTab,
  type OperationsWindow,
} from './policy'

const logPageSchema = z.object({
  items: z.array(usageLogSchema).max(50),
  total: z.number().int().nonnegative(),
})
const logStatsSchema = z.object({
  quota: z.number().finite(),
  rpm: z.number().nonnegative().finite(),
  tpm: z.number().nonnegative().finite(),
})
export type ScreenLogPage = z.infer<typeof logPageSchema>
export type ScreenLogStats = z.infer<typeof logStatsSchema>

async function readLogs(
  window: OperationsWindow,
  type: 2 | 5,
  signal: AbortSignal,
  modelName?: string
): Promise<ScreenLogPage> {
  const response = await api.get('/api/log/', {
    params: {
      ...window,
      type,
      p: 1,
      page_size: 20,
      ...(modelName ? { model_name: modelName } : {}),
    },
    signal,
    disableDuplicate: true,
  })
  const envelope = requireServerSuccess(response.data)
  return logPageSchema.parse(envelope.data)
}
async function readUsageStats(
  window: OperationsWindow,
  signal: AbortSignal,
  modelName?: string
): Promise<ScreenLogStats> {
  const response = await api.get('/api/log/stat', {
    params: {
      ...window,
      type: 2,
      ...(modelName ? { model_name: modelName } : {}),
    },
    signal,
    disableDuplicate: true,
  })
  const envelope = requireServerSuccess(response.data)
  return logStatsSchema.parse(envelope.data)
}

export async function getOperationsSnapshot(
  tab: OperationsTab,
  window: OperationsWindow,
  signal: AbortSignal,
  selectedModel?: string
) {
  const modelName = supportsOperationsModelFilter(tab)
    ? selectedModel?.trim() || undefined
    : undefined
  if (!validOperationsFilters(tab, window, modelName)) {
    throw new Error('Invalid parameters')
  }
  const controller = new AbortController()
  const abort = () => controller.abort()
  signal.addEventListener('abort', abort, { once: true })
  if (signal.aborted) controller.abort()
  const requestSignal = controller.signal
  try {
    switch (tab) {
      case 'profit':
        return {
          tab,
          window,
          report: await getModelProfits(
            { ...window, ...(modelName ? { model_name: modelName } : {}) },
            requestSignal
          ),
        } as const
      case 'concurrency':
        return {
          tab,
          window,
          report: await getMediaConcurrency(requestSignal),
        } as const
      case 'image':
      case 'video':
        return {
          tab,
          window,
          report: await getMediaOperations(
            {
              ...window,
              media_kind: tab,
              ...(modelName ? { model: modelName } : {}),
            },
            requestSignal
          ),
        } as const
      case 'usage': {
        const [logs, stats] = await Promise.all([
          readLogs(window, 2, requestSignal, modelName),
          readUsageStats(window, requestSignal, modelName),
        ])
        return { tab, window, logs, stats } as const
      }
      case 'errors':
        return {
          tab,
          window,
          logs: await readLogs(window, 5, requestSignal, modelName),
        } as const
      case 'activity': {
        const [summary, sessions] = await Promise.all([
          getActivitySummary(window, requestSignal),
          getActivitySessions(
            { scope: 'live', status: 'all', p: 1, page_size: 20 },
            requestSignal
          ),
        ])
        return { tab, window, summary, sessions } as const
      }
    }
  } catch (error) {
    controller.abort()
    throw error
  } finally {
    signal.removeEventListener('abort', abort)
  }
}
export type OperationsSnapshot = Awaited<
  ReturnType<typeof getOperationsSnapshot>
>
