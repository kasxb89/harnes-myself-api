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

export const CUSTOMER_SERVICE_QUERY_KEY = ['customer-service'] as const
const ASSET_PATH = /^\/api\/creation\/assets\/(cc_[a-f0-9]{64})$/

export function customerServiceAssetPath(id: string | null): string {
  // Keep stored paths canonical when a deployment rewrites quoted API URLs.
  return id
    ? ['api', 'creation', 'assets', id].reduce(
        (path, segment) => `${path}/${segment}`,
        ''
      )
    : ''
}

export function customerServiceAssetId(value: string): string | null {
  return ASSET_PATH.exec(value)?.[1] ?? null
}

function isPlainCustomerServiceText(value: string): boolean {
  for (const character of value) {
    const code = character.charCodeAt(0)
    if (code < 32 || (code >= 127 && code <= 159)) return false
  }
  return true
}

export const customerServiceSchema = z
  .object({
    schema_version: z.literal(1),
    enabled: z.boolean(),
    qr_code_url: z
      .string()
      .refine(
        (value) => value === '' || ASSET_PATH.test(value),
        'Upload a PNG, JPEG or WebP customer service QR code'
      ),
    title: z
      .string()
      .max(64, 'Customer service title must be at most 64 characters')
      .refine(
        isPlainCustomerServiceText,
        'Customer service text must not contain control characters'
      ),
    description: z
      .string()
      .max(256, 'Customer service description must be at most 256 characters')
      .refine(
        isPlainCustomerServiceText,
        'Customer service text must not contain control characters'
      ),
  })
  .strict()
  .superRefine((value, context) => {
    if (value.enabled && !value.qr_code_url) {
      context.addIssue({
        code: 'custom',
        path: ['qr_code_url'],
        message: 'Upload a customer service QR code before enabling it',
      })
    }
  })

export type CustomerService = z.infer<typeof customerServiceSchema>
export const EMPTY_CUSTOMER_SERVICE: CustomerService = {
  schema_version: 1,
  enabled: false,
  qr_code_url: '',
  title: '',
  description: '',
}
