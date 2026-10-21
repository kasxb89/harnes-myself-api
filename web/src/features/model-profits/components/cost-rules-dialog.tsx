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
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { toIntlLocale } from '@/i18n/languages'
import { handleServerError } from '@/lib/handle-server-error'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { getCostRules, saveCostRules } from '../api'
import { formatProfitCny } from '../lib/format'
import { shanghaiDateTime } from '../lib/period'
import type { CostRule } from '../types'
import { CostRuleForm } from './cost-rule-form'

export function CostRulesDialog() {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const role = useAuthStore((state) => state.auth.user?.role ?? ROLE.GUEST)
  const [open, setOpen] = useState(false)
  const client = useQueryClient()
  const rules = useQuery({
    queryKey: ['model-profit-cost-rules'],
    queryFn: getCostRules,
    enabled: open && role >= ROLE.SUPER_ADMIN,
    retry: false,
  })
  const save = useMutation({
    mutationFn: async (rule: CostRule) => {
      if (!rules.data) throw new Error(t('Failed to load upstream costs'))
      return saveCostRules({
        ...rules.data,
        rules: [...rules.data.rules, rule],
      })
    },
    onSuccess: async (data) => {
      client.setQueryData(['model-profit-cost-rules'], data)
      await client.invalidateQueries({ queryKey: ['model-profits'] })
      toast.success(t('Saved'))
    },
    onError: (error) => {
      handleServerError(error)
      void rules.refetch()
    },
  })
  if (role < ROLE.SUPER_ADMIN) return null
  return (
    <Dialog
      open={open}
      onOpenChange={setOpen}
      title={t('Upstream cost rules')}
      description={t(
        'Add CNY supplier price rules by channel and exact model. Existing versions cannot be changed or deleted. Missing costs remain unknown.'
      )}
      trigger={
        <Button variant='outline'>{t('Configure upstream costs')}</Button>
      }
    >
      <div className='space-y-5'>
        {rules.isPending && <LoadingState />}
        {rules.isError && (
          <ErrorState
            title={t('Failed to load upstream costs')}
            onRetry={() => {
              void rules.refetch()
            }}
          />
        )}
        {rules.data && !rules.isError && (
          <>
            <div
              className='max-h-48 space-y-2 overflow-auto rounded-lg border p-3'
              aria-label={t('Existing cost versions')}
            >
              {rules.data.rules.length === 0 && (
                <p className='text-muted-foreground text-sm'>
                  {t('No upstream costs configured')}
                </p>
              )}
              {rules.data.rules.map((rule) => (
                <div
                  key={`${rule.channel_id}:${rule.model_name}:${rule.effective_from}`}
                  className='space-y-1 border-b pb-2 text-xs last:border-0'
                >
                  <div className='font-medium break-all'>
                    #{rule.channel_id} · {rule.model_name}
                  </div>
                  {rule.effective_until !== undefined && (
                    <div>
                      {t(
                        'Cost valid until (Shanghai time, exclusive, optional)'
                      )}
                      :{' '}
                      {shanghaiDateTime(rule.effective_until * 1000).replace(
                        'T',
                        ' '
                      )}
                    </div>
                  )}
                  <div>
                    {t('Effective from (Shanghai time)')}:{' '}
                    {shanghaiDateTime(rule.effective_from * 1000).replace(
                      'T',
                      ' '
                    )}
                  </div>
                  {rule.kind === 'token' && (
                    <div>{`${t('Input')}: ${formatProfitCny(rule.input_cost_per_million_cny, locale)} · ${t('Cache Read')}: ${formatProfitCny(rule.cache_cost_per_million_cny, locale)} · ${t('Output')}: ${formatProfitCny(rule.output_cost_per_million_cny, locale)} / ${t('Per million tokens')}`}</div>
                  )}
                  {rule.kind === 'image_resolution' && (
                    <div>{`1K: ${formatProfitCny(rule.resolution_costs_cny?.['1k'], locale)} · 2K: ${formatProfitCny(rule.resolution_costs_cny?.['2k'], locale)} · 4K: ${formatProfitCny(rule.resolution_costs_cny?.['4k'], locale)} / ${t('Per image')}`}</div>
                  )}
                  {rule.kind !== 'token' &&
                    rule.kind !== 'image_resolution' && (
                      <div>{`${formatProfitCny(rule.unit_cost_cny, locale)} / ${t({ request: 'Per request', second: 'Per second', image: 'Per image' }[rule.kind])}`}</div>
                    )}
                  {rule.deepseek_peak_multiplier && (
                    <div>{t('DeepSeek peak multiplier (optional)')}: 2</div>
                  )}
                  <div className='text-muted-foreground break-all'>
                    {rule.source}
                  </div>
                </div>
              ))}
            </div>
            <CostRuleForm
              onSave={(rule) => save.mutateAsync(rule).then(() => undefined)}
              saving={save.isPending}
            />
          </>
        )}
      </div>
    </Dialog>
  )
}
