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

export const ycapiModelSchema = z.object({
  id: z.string().max(256).startsWith('ycapi-'),
  upstreamModelId: z.string().min(1).max(250),
  upstreamPublicId: z.string().optional(),
  kind: z.enum(['image', 'video', 'text', 'audio']),
  name: z.string().max(512),
  catalogName: z.string().optional(),
  nameEn: z.string().optional(),
  nameZh: z.string().optional(),
  provider: z.string().max(128),
  description: z.string(),
  descriptionEn: z.string().optional(),
  descriptionZh: z.string().optional(),
  inputHintZh: z.string().optional(),
  catalogBillingMode: z.string().max(128),
  catalogBillingUnit: z.string().max(128),
  iconKey: z.string().optional(),
  tags: z.array(z.string()),
  capabilities: z.array(z.string()),
  maxPromptChars: z.number().nonnegative().optional(),
  paramSchema: z.record(z.string(), z.unknown()),
  linkages: z.array(z.unknown()),
  frameReferenceExclusive: z.boolean().optional(),
  retailOffers: z.array(
    z.object({ id: z.string(), params: z.record(z.string(), z.unknown()) })
  ),
  priceRows: z
    .array(
      z.object({
        modelId: z.string(),
        quoteId: z.string().optional(),
        direction: z.enum(['input', 'output']).optional(),
        params: z.record(z.string(), z.unknown()),
        unit: z.string(),
        procurementCny: z.number().nonnegative(),
        retailCny: z.number().nonnegative(),
        officialCny: z.number().positive(),
        procurementZhe: z.number().nonnegative(),
        retailZhe: z.number().nonnegative().lt(10),
        laneId: z.string().max(500),
        nativeQuantity: z.number().positive().optional(),
        retailTotalCny: z.number().nonnegative().optional(),
        procurementTotalCny: z.number().nonnegative().optional(),
        officialTotalCny: z.number().positive().optional(),
        source: z.string().url().startsWith('https://'),
        officialConditions: z.string().optional(),
        rateVerifiedAt: z.string(),
      })
    )
    .optional(),
})

export const ycapiCatalogSchema = z.object({
  schema: z.literal(1),
  version: z.string(),
  checkedAt: z.string(),
  count: z.number(),
  priceRows: z.number(),
  mode: z.literal('local-preview'),
  generationEnabled: z.literal(false),
  officialUsdCny: z.number().positive(),
  exclusions: z.array(
    z.object({ id: z.string(), upstreamModelId: z.string() })
  ),
  models: z.array(ycapiModelSchema),
})
export type YcapiModel = z.infer<typeof ycapiModelSchema>
export type YcapiCatalog = z.infer<typeof ycapiCatalogSchema>
export type QuotePreview = {
  modelId: string
  specId: string
  retailCny: string
  procurementCny: string
  officialCny: string
  fingerprint: string
  endpoint: string
  upstreamBody: Record<string, unknown>
  generationEnabled: false
  estimated: boolean
}
