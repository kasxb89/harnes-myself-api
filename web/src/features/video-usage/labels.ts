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
export const statusLabels: Record<string, string> = {
  NOT_START: 'Recorded not started',
  SUBMITTED: 'Submitted',
  QUEUED: 'Upstream-reported queued',
  IN_PROGRESS: 'In progress',
  SUCCESS: 'Success',
  FAILURE: 'Failed',
  UNKNOWN: 'Recorded unknown states',
}

export function videoStatusLabelKey(status: string): string {
  return Object.hasOwn(statusLabels, status)
    ? statusLabels[status]
    : 'Recorded unknown states'
}
