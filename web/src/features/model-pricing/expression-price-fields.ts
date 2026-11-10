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
import { compileBillingExpression } from '@/features/pricing/lib/billing-expression/parser'
import { evaluateBillingExpression } from '@/features/pricing/lib/billing-expression/runtime'
import type { ExpressionNode } from '@/features/pricing/lib/billing-expression/types'
import type { BillingUsageSchema } from '@/features/pricing/types'

export type PriceCondition = { node: ExpressionNode; matches: boolean }
export type PriceQuantity = { field: string; free?: number; block?: number }
export type ExpressionPriceField = {
  node: ExpressionNode
  tier: string
  quantity?: PriceQuantity
  conditions: PriceCondition[]
  adjustments: ExpressionNode[]
  amount: number
  scale: number
  wrapQuantity?: boolean
  fixed?: boolean
}

function constant(node: ExpressionNode, source: string): number | null {
  const numeric = (part: ExpressionNode): boolean => {
    if (part.kind === 'literal') return typeof part.value === 'number'
    if (part.kind === 'unary' && ['+', '-'].includes(part.operator)) {
      return numeric(part.operand)
    }
    return (
      part.kind === 'binary' &&
      ['+', '-', '*', '/'].includes(part.operator) &&
      numeric(part.left) &&
      numeric(part.right)
    )
  }
  if (!numeric(node)) return null
  const result = evaluateBillingExpression(source.slice(node.start, node.end))
  return result.status === 'success' &&
    Number.isFinite(result.cost) &&
    result.cost >= 0
    ? result.cost
    : null
}

function quantity(
  node: ExpressionNode,
  schema?: BillingUsageSchema
): PriceQuantity | null {
  if (node.kind === 'variable' && node.name !== 'len') {
    return { field: node.name }
  }
  if (
    node.kind === 'call' &&
    node.name === 'u' &&
    node.args[0].kind === 'literal' &&
    typeof node.args[0].value === 'string'
  ) {
    const field = node.args[0].value
    return schema?.[field]?.type === 'number' && schema[field].unit
      ? { field }
      : null
  }
  // Free reference images remain a quantity rule, never a price coefficient.
  if (node.kind === 'call' && node.name === 'max') {
    const [difference, zero] = node.args
    if (
      zero.kind === 'literal' &&
      zero.value === 0 &&
      difference.kind === 'binary' &&
      difference.operator === '-' &&
      difference.right.kind === 'literal' &&
      typeof difference.right.value === 'number' &&
      difference.right.value >= 0
    ) {
      const base = quantity(difference.left, schema)
      if (base && base.free === undefined) {
        return { ...base, free: difference.right.value }
      }
    }
  }
  if (
    node.kind === 'call' &&
    node.name === 'ceil' &&
    node.args[0].kind === 'binary' &&
    node.args[0].operator === '/'
  ) {
    const { left, right } = node.args[0]
    const base = quantity(left, schema)
    if (
      base &&
      right.kind === 'literal' &&
      typeof right.value === 'number' &&
      right.value > 0
    ) {
      return { ...base, block: right.value }
    }
  }
  return null
}

/** Lossless price slots over the existing AST. Never regenerate a tariff or fit sampled prices. */
export function readExpressionPriceFields(
  source: string,
  schema?: BillingUsageSchema
): ExpressionPriceField[] | null {
  const compiled = compileBillingExpression(source)
  if (compiled.status !== 'ready') return null
  const fields: ExpressionPriceField[] = []
  type Context = Omit<ExpressionPriceField, 'node' | 'amount' | 'wrapQuantity'>
  const add = (
    node: ExpressionNode,
    value: number,
    context: Context,
    wrapQuantity = false
  ) => {
    const tokenScale =
      context.quantity && schema?.[context.quantity.field]?.unit === 'token'
        ? 1_000_000
        : 1
    const scale = context.scale * tokenScale
    if (
      !Number.isFinite(scale) ||
      scale <= 0 ||
      !Number.isFinite(value * scale)
    ) {
      return false
    }
    fields.push({
      ...context,
      node,
      amount: value * scale,
      scale,
      wrapQuantity,
    })
    return true
  }
  const multiplier = (node: ExpressionNode): boolean =>
    node.kind === 'conditional' &&
    constant(node.yes, source) !== null &&
    constant(node.no, source) !== null
  const walk = (node: ExpressionNode, context: Context): boolean => {
    if (fields.length > 256) return false
    const value = constant(node, source)
    if (value !== null) return add(node, value, context)
    if (
      node.kind === 'call' &&
      node.name === 'tier' &&
      node.args[0].kind === 'literal' &&
      typeof node.args[0].value === 'string'
    ) {
      return walk(node.args[1], { ...context, tier: node.args[0].value })
    }
    if (node.kind === 'call' && node.name === 'fixed') {
      const price = constant(node.args[0], source)
      return (
        price !== null && add(node.args[0], price, { ...context, fixed: true })
      )
    }
    if (node.kind === 'conditional') {
      return (
        walk(node.yes, {
          ...context,
          conditions: [
            ...context.conditions,
            { node: node.condition, matches: true },
          ],
        }) &&
        walk(node.no, {
          ...context,
          conditions: [
            ...context.conditions,
            { node: node.condition, matches: false },
          ],
        })
      )
    }
    if (node.kind === 'binary') {
      if (node.operator === '+') {
        return walk(node.left, context) && walk(node.right, context)
      }
      if (node.operator === '*') {
        const pairs = [
          [node.left, node.right],
          [node.right, node.left],
        ]
        for (const [probe, other] of pairs) {
          const usage = quantity(probe, schema)
          if (usage && !context.quantity) {
            return walk(other, { ...context, quantity: usage })
          }
        }
        for (const [probe, other] of pairs) {
          if (multiplier(probe)) {
            return walk(other, {
              ...context,
              adjustments: [...context.adjustments, probe],
            })
          }
        }
        for (const [probe, other] of pairs) {
          const factor = constant(probe, source)
          if (factor !== null && factor > 0) {
            return walk(other, { ...context, scale: context.scale * factor })
          }
        }
      }
      if (node.operator === '/') {
        const denominator = constant(node.right, source)
        if (denominator !== null && denominator > 0) {
          return walk(node.left, {
            ...context,
            scale: context.scale / denominator,
          })
        }
      }
    }
    const usage = quantity(node, schema)
    return Boolean(
      usage &&
      !context.quantity &&
      add(node, 1, { ...context, quantity: usage }, true)
    )
  }
  return walk(compiled.ast, {
    tier: '',
    conditions: [],
    adjustments: [],
    scale: 1,
  }) && fields.length
    ? fields
    : null
}

export function replaceExpressionPrice(
  source: string,
  field: ExpressionPriceField,
  amount: number
): string | null {
  const value = amount / field.scale
  if (!Number.isFinite(value) || value < 0) return null
  // A decimal replacement retains floating-point money semantics in subsequent divisions.
  const literal =
    Number.isInteger(value) && !/[eE]/.test(String(value))
      ? `${value}.0`
      : String(value)
  const replacement = field.wrapQuantity
    ? `(${source.slice(field.node.start, field.node.end)}) * ${literal}`
    : literal
  const next =
    source.slice(0, field.node.start) +
    replacement +
    source.slice(field.node.end)
  return compileBillingExpression(next).status === 'ready' ? next : null
}
