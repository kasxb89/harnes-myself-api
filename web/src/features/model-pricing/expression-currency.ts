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
import { formatPricingNumber } from '@/features/system-settings/models/pricing-format'

// Convert monetary results, never conditions, usage facts or request multipliers.
// Unknown frontend grammar is deliberately not guessed. The backend stays USD.
export function convertExpressionCurrency(
  source: string,
  factor: number
): string | null {
  if (!Number.isFinite(factor) || factor <= 0) return null
  if (!source.trim() || factor === 1) return source
  const compiled = compileBillingExpression(source)
  if (compiled.status !== 'ready') return null
  const rules = new Set(compiled.requestRules.map((rule) => rule.node))
  const original = (node: ExpressionNode) => source.slice(node.start, node.end)
  const replace = (node: ExpressionNode, parts: [ExpressionNode, string][]) => {
    let result = original(node)
    for (const [child, text] of parts.sort((a, b) => b[0].start - a[0].start)) {
      result =
        result.slice(0, child.start - node.start) +
        text +
        result.slice(child.end - node.start)
    }
    return result
  }
  const numericConstant = (node: ExpressionNode): boolean => {
    if (node.kind === 'literal') return typeof node.value === 'number'
    if (node.kind === 'unary' && ['+', '-'].includes(node.operator)) {
      return numericConstant(node.operand)
    }
    return (
      node.kind === 'binary' &&
      ['+', '-', '*', '/'].includes(node.operator) &&
      numericConstant(node.left) &&
      numericConstant(node.right)
    )
  }
  const constant = (node: ExpressionNode): number | undefined => {
    if (!numericConstant(node)) return undefined
    const result = evaluateBillingExpression(original(node))
    return result.status === 'success' ? result.cost : undefined
  }
  const hasTier = (node: ExpressionNode): boolean => {
    if (node.kind === 'call') {
      return (
        node.name === 'tier' || node.name === 'fixed' || node.args.some(hasTier)
      )
    }
    if (node.kind === 'binary') return hasTier(node.left) || hasTier(node.right)
    if (node.kind === 'conditional') {
      return hasTier(node.yes) || hasTier(node.no)
    }
    if (node.kind === 'unary') return hasTier(node.operand)
    return false
  }
  const money = (node: ExpressionNode): string => {
    const value = constant(node)
    if (value !== undefined && Number.isFinite(value * factor)) {
      const amount = value * factor
      const formatted = formatPricingNumber(amount)
      // Clean floating-point display noise, retaining genuine high precision.
      if (
        Math.abs(Number(formatted) - amount) <=
        Math.abs(amount) * Number.EPSILON * 4
      ) {
        return formatted
      }
      return String(amount)
    }
    if (node.kind === 'conditional') {
      return replace(node, [
        [node.yes, money(node.yes)],
        [node.no, money(node.no)],
      ])
    }
    if (node.kind === 'unary' && ['+', '-'].includes(node.operator)) {
      return replace(node, [[node.operand, money(node.operand)]])
    }
    if (node.kind === 'call') {
      if (node.name === 'tier') {
        return replace(node, [[node.args[1], money(node.args[1])]])
      }
      if (node.name === 'fixed') {
        return replace(node, [[node.args[0], money(node.args[0])]])
      }
    }
    if (node.kind === 'binary') {
      if (['+', '-'].includes(node.operator)) {
        return replace(node, [
          [node.left, money(node.left)],
          [node.right, money(node.right)],
        ])
      }
      if (node.operator === '*') {
        let chosen: ExpressionNode | undefined
        if (rules.has(node.left)) chosen = node.right
        else if (rules.has(node.right)) chosen = node.left
        else if (hasTier(node.left) !== hasTier(node.right)) {
          chosen = hasTier(node.left) ? node.left : node.right
        } else if (constant(node.right) !== undefined) chosen = node.right
        else if (constant(node.left) !== undefined) chosen = node.left
        if (chosen) return replace(node, [[chosen, money(chosen)]])
      }
    }
    // A non-homogeneous function such as ceil must be scaled after evaluation.
    return `(${original(node)}) * ${factor}`
  }
  const result =
    source.slice(0, compiled.ast.start) +
    money(compiled.ast) +
    source.slice(compiled.ast.end)
  return compileBillingExpression(result).status === 'ready' ? result : null
}
