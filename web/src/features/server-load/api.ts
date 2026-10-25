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

import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

const counter = z.number().finite().nonnegative()
const nullableCounter = counter.nullable()
const percent = counter.max(100).nullable()
const limitStatus = z.enum(['unknown', 'unlimited', 'limited'])
const pool = z.object({
  status: z.enum(['up', 'down', 'unknown']),
  open_connections: nullableCounter,
  in_use: nullableCounter,
  idle: nullableCounter,
  max_open_connections: nullableCounter,
  wait_count: nullableCounter,
  wait_duration_ms: nullableCounter,
})
export const serverLoadSchema = z.object({
  schema_version: z.literal(1),
  sampled_at: counter.int(),
  refresh_interval_seconds: z.literal(10),
  scope: z.enum(['host', 'container']),
  host_visibility: z.enum(['system', 'partial']),
  runtime_os: z.string(),
  cpu: z.object({
    usage_percent: percent,
    logical_cores: counter.int(),
    load_1: nullableCounter,
    load_5: nullableCounter,
    load_15: nullableCounter,
  }),
  memory: z.object({
    total_bytes: nullableCounter,
    available_bytes: nullableCounter,
    used_percent: percent,
  }),
  disk: z.object({
    scope: z.literal('service_storage'),
    total_bytes: nullableCounter,
    available_bytes: nullableCounter,
    used_bytes: nullableCounter,
    used_percent: percent,
  }),
  container: z.object({
    detected: z.boolean(),
    cgroup_version: z.string().nullable(),
    memory_current_bytes: nullableCounter,
    memory_limit_bytes: nullableCounter,
    memory_limit_status: limitStatus,
    cpu_limit_cores: nullableCounter,
    cpu_limit_status: limitStatus,
  }),
  process: z.object({
    rss_bytes: nullableCounter,
    goroutines: counter.int(),
    go_heap_alloc_bytes: counter,
    go_heap_sys_bytes: counter,
    go_sys_bytes: counter,
    gc_cycles: counter.int(),
  }),
  service: z.object({
    status: z.literal('up'),
    version: z.string(),
    started_at: counter.int(),
    uptime_seconds: counter.int(),
  }),
  database: z.object({ main: pool, logs: pool, shared: z.boolean() }),
  thresholds: z.object({
    guard_enabled: z.boolean(),
    cpu_percent: counter,
    memory_percent: counter.max(100),
    disk_percent: counter.max(100),
  }),
})
export type ServerLoadSnapshot = z.infer<typeof serverLoadSchema>
export type DatabasePool = ServerLoadSnapshot['database']['main']

export async function getServerLoad(
  signal: AbortSignal
): Promise<ServerLoadSnapshot> {
  const response = await api.get('/api/operations/server-load', {
    signal,
    timeout: 10_000,
    disableDuplicate: true,
  })
  return serverLoadSchema.parse(requireServerSuccess(response.data).data)
}
