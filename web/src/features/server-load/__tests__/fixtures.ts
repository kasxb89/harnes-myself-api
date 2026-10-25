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
import type { ServerLoadSnapshot } from '../api'

export function serverLoadFixture(sampledAt = 1792868400): ServerLoadSnapshot {
  return {
    schema_version: 1 as const,
    sampled_at: sampledAt,
    refresh_interval_seconds: 10,
    scope: 'container' as const,
    host_visibility: 'partial' as const,
    runtime_os: 'linux',
    cpu: {
      usage_percent: 27,
      logical_cores: 4,
      load_1: 0.4,
      load_5: 0.3,
      load_15: 0.2,
    },
    memory: {
      total_bytes: 8 * 1024 ** 3,
      available_bytes: 3 * 1024 ** 3,
      used_percent: 62.5,
    },
    disk: {
      scope: 'service_storage' as const,
      total_bytes: 100 * 1024 ** 3,
      available_bytes: 40 * 1024 ** 3,
      used_bytes: 60 * 1024 ** 3,
      used_percent: 60,
    },
    container: {
      detected: true,
      cgroup_version: 'v2',
      memory_current_bytes: 300 * 1024 ** 2,
      memory_limit_bytes: 1024 ** 3,
      memory_limit_status: 'limited' as const,
      cpu_limit_cores: 2,
      cpu_limit_status: 'limited' as const,
    },
    process: {
      rss_bytes: 256 * 1024 ** 2,
      goroutines: 120,
      go_heap_alloc_bytes: 48 * 1024 ** 2,
      go_heap_sys_bytes: 80 * 1024 ** 2,
      go_sys_bytes: 120 * 1024 ** 2,
      gc_cycles: 15,
    },
    service: {
      status: 'up' as const,
      version: 'test-version',
      started_at: sampledAt - 3600,
      uptime_seconds: 3600,
    },
    database: {
      main: {
        status: 'up' as const,
        open_connections: 6,
        in_use: 2,
        idle: 4,
        max_open_connections: 100,
        wait_count: 3,
        wait_duration_ms: 12,
      },
      logs: {
        status: 'unknown' as const,
        open_connections: null,
        in_use: null,
        idle: null,
        max_open_connections: null,
        wait_count: null,
        wait_duration_ms: null,
      },
      shared: false,
    },
    thresholds: {
      guard_enabled: true,
      cpu_percent: 90,
      memory_percent: 90,
      disk_percent: 93,
    },
  }
}
