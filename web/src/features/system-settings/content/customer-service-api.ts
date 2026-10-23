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
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import {
  customerServiceSchema,
  type CustomerService,
} from './lib/customer-service'

type Response = { success: boolean; message?: string; data: CustomerService }

export async function loadCustomerService(
  signal?: AbortSignal
): Promise<CustomerService> {
  const response = await api.get<Response>('/api/customer-service', {
    signal,
    disableDuplicate: true,
  })
  return customerServiceSchema.parse(requireServerSuccess(response.data).data)
}

export async function saveCustomerService(
  value: CustomerService
): Promise<CustomerService> {
  const response = await api.put<Response>(
    '/api/customer-service',
    customerServiceSchema.parse(value)
  )
  return customerServiceSchema.parse(requireServerSuccess(response.data).data)
}
