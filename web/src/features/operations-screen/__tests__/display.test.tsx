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
import { render, screen, within } from '@testing-library/react'
import { expect, it } from 'vitest'

import { usageLogSchema } from '@/features/usage-logs/data/schema'

import { OperationsConcurrencyPanel } from '../components/concurrency-panel'
import { OperationsLogsPanel } from '../components/logs-panel'

it('keeps missing account capacity unknown instead of presenting an empty pool list as measured availability', () => {
  render(
    <OperationsConcurrencyPanel
      report={{
        schema_version: 1,
        revision: 'empty',
        defaults: { video: 2, image: 4 },
        pool_overrides: [],
        pools: [],
      }}
    />
  )
  expect(
    within(
      screen.getByRole('group', { name: 'Available admission slots' })
    ).getByText('Unknown')
  ).toBeVisible()
  expect(
    screen.getByText('No registered image or video account pools')
  ).toBeVisible()
})
it('separates mapped gateway status from observed upstream status and preserves unknown causes', () => {
  const row = usageLogSchema.parse({
    id: 1,
    user_id: 7,
    type: 5,
    created_at: 1792810800,
    content: 'unclassified fixture failure',
    other: JSON.stringify({
      status_code: 502,
      status_code_origin: 'gateway_task_mapping',
      upstream_http_status: 429,
    }),
  })
  render(
    <OperationsLogsPanel
      snapshot={{
        tab: 'errors',
        window: { start_timestamp: 1792810500, end_timestamp: 1792810800 },
        logs: { total: 123, items: [row, { ...row, id: 2, other: '' }] },
      }}
    />
  )
  const table = screen.getByRole('table', { name: 'Recent API error records' })
  expect(within(table).getByText('Specific cause not identified')).toBeVisible()
  expect(within(table).getByText('502 (Task mapping)')).toBeVisible()
  expect(within(table).getByText('Upstream HTTP Status: 429')).toBeVisible()
  expect(
    screen.getByText('Showing the latest 2 of 123 matching log records.')
  ).toBeVisible()
  expect(screen.queryByText('123%')).not.toBeInTheDocument()
})
it('shows recorded zero counts while long model and error content have explicit wrapping bounds', () => {
  const model = 'model-'.repeat(80)
  const content = 'failure'.repeat(200)
  const row = usageLogSchema.parse({
    id: 1,
    user_id: 7,
    type: 5,
    created_at: 1792810800,
    content,
    model_name: model,
  })
  render(
    <OperationsLogsPanel
      snapshot={{
        tab: 'errors',
        window: { start_timestamp: 1792810500, end_timestamp: 1792810800 },
        logs: { total: 1, items: [row] },
      }}
    />
  )
  expect(screen.getByText(model)).toHaveClass('break-all', 'max-w-60')
  expect(screen.getByText(content)).toHaveClass(
    'whitespace-pre-wrap',
    'break-all',
    'max-w-96'
  )
})
