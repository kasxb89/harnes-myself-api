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
import { cleanup, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createInstance } from 'i18next'
import { I18nextProvider } from 'react-i18next'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

import en from '@/i18n/locales/en.json'

import VideoTrends from '../components/trends'
import { videoUsageSchema } from '../schema'
import { videoPayload } from './fixtures'

const i18n = createInstance()
beforeEach(async () => {
  await i18n.init({
    lng: 'en',
    fallbackLng: 'en',
    keySeparator: false,
    nsSeparator: false,
    resources: { en },
  })
})
afterEach(() => {
  cleanup()
  vi.restoreAllMocks()
})
function renderTrends(ingress = videoPayload.ingress) {
  return render(
    <I18nextProvider i18n={i18n}>
      <VideoTrends
        trends={videoPayload.trends}
        ingress={ingress}
        onRecentRange={vi.fn()}
      />
    </I18nextProvider>
  )
}
it('clipped task buckets display server rates, actual seconds and missing peaks without recomputing', async () => {
  renderTrends()
  await userEvent.click(screen.getByText('View bucket values'))
  const table = screen.getByRole('table', { name: 'Task trend bucket values' })
  const rows = within(table).getAllByRole('row').slice(1)
  expect(
    within(rows[0])
      .getAllByRole('cell')
      .map((cell) => cell.textContent)
      .slice(1, 5)
  ).toEqual(['30', '2', '0.066667', '3'])
  expect(
    within(rows[1])
      .getAllByRole('cell')
      .map((cell) => cell.textContent)
      .slice(1, 5)
  ).toEqual(['60', '4', '0.066667', 'Unknown'])
  expect(
    within(rows[2])
      .getAllByRole('cell')
      .map((cell) => cell.textContent)
      .slice(1, 5)
  ).toEqual(['15', '1', '0.066667', '1'])
  expect(within(rows[0]).getAllByRole('cell')[0]).toHaveTextContent(/^\[.*\)$/)
  expect(
    screen.getByText(
      'Recorded accepted tasks per second, not HTTP ingress QPS.'
    )
  ).toBeVisible()
  expect(rows[0]).toHaveTextContent(
    'Excluded 1: unknown 1, invalid time 0, missing finish 0, zero length 0.'
  )
  expect(
    screen.getByText('Excluded intervals may hide additional in-flight tasks.')
  ).toBeVisible()
})
it('partial and unavailable ingress buckets remain unknown while fully observed zero is displayed as zero', async () => {
  const points = videoPayload.ingress.points.map((point) =>
    point.coverage === 'complete'
      ? { ...point, submit_requests: 0, submit_qps: 0 }
      : point
  )
  renderTrends({ ...videoPayload.ingress, points })
  await userEvent.click(screen.getByText('View bucket values'))
  const table = screen.getByRole('table', { name: 'Ingress bucket values' })
  const rows = within(table).getAllByRole('row').slice(1)
  expect(rows[0]).toHaveTextContent('Not observed')
  expect(within(rows[0]).getAllByRole('cell')[2]).toHaveTextContent(
    'Unknown / Unknown'
  )
  expect(rows[1]).toHaveTextContent('40 / 60 s')
  expect(within(rows[1]).getAllByRole('cell')[2]).toHaveTextContent(
    'Unknown / Unknown'
  )
  expect(within(rows[2]).getAllByRole('cell')[2]).toHaveTextContent('0 / 0')
  expect(
    screen.getByText(
      'Only the time range applies. Model, user, client source, channel and provider filters do not apply to this chart.'
    )
  ).toBeVisible()
  expect(
    screen.getByText(
      'Only recognized video paths that reach this Go application are counted; requests rejected by Nginx, TLS or HTTP parsing are not observed.'
    )
  ).toBeVisible()
})
it('a process without complete buckets explains the one-hour and restart limits with a recent-window action', () => {
  renderTrends({
    ...videoPayload.ingress,
    points: videoPayload.ingress.points.slice(0, 2),
  })
  expect(screen.getByRole('status')).toHaveTextContent(
    'No complete ingress buckets.'
  )
  expect(
    screen.getByText(
      'In-memory retention is 1 hour, with no historical persistence. Restarts reset coverage; partial and unobserved buckets are unknown.'
    )
  ).toBeVisible()
  expect(screen.getByRole('button', { name: 'Last 5 minutes' })).toBeEnabled()
})
it('an incomplete ingress bucket with numeric counts is rejected instead of inventing historical QPS', () => {
  expect(
    videoUsageSchema.safeParse({
      ...videoPayload,
      ingress: {
        ...videoPayload.ingress,
        points: [{ ...videoPayload.ingress.points[0], submit_requests: 0 }],
      },
    }).success
  ).toBe(false)
})
