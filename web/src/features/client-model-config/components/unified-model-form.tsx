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
import { zodResolver } from '@hookform/resolvers/zod'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useMemo, useRef, useState } from 'react'
import { Controller, useFieldArray, useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { ComboboxInput } from '@/components/ui/combobox-input'
import { FieldError } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { useModelPricing } from '@/features/model-pricing/api'
import { pricingFromDraft, pricingRow } from '@/features/model-pricing/pricing'
import { SettingsSection } from '@/features/system-settings/components/settings-section'
import {
  ModelPricingEditorPanel,
  type ModelPricingEditorPanelHandle,
} from '@/features/system-settings/models/model-pricing-sheet'
import { statusQueryOptions } from '@/lib/status-query'

import {
  unifiedModelFormSchema,
  type UnifiedModelFormValues,
} from '../lib/schema'
import type {
  UnifiedModelDefinition,
  UnifiedModelEntry,
  UnifiedModelSnapshot,
  OfficialReferenceConfiguration,
} from '../unified-api'
import { CostRulesTable } from './cost-rules-table'
import { MediaParametersForm } from './media-parameters-form'
import { OfficialReferencesForm } from './official-references-form'

export function UnifiedModelForm(props: {
  entry: UnifiedModelEntry
  channels: UnifiedModelSnapshot['channels']
  pending: boolean
  onSave: (definition: UnifiedModelDefinition) => Promise<void>
  onDirtyChange: (dirty: boolean) => void
}) {
  const { t } = useTranslation()
  const containerRef = useRef<HTMLDivElement>(null)
  const cache = useQueryClient()
  const status = useQuery(statusQueryOptions)
  const rawRate = status.data?.usd_exchange_rate
  const rate =
    typeof rawRate === 'number' || typeof rawRate === 'string'
      ? Number(rawRate)
      : Number.NaN
  const cnyCurrency = useMemo(
    () => ({ label: 'CNY', symbol: '¥', exchangeRate: rate }),
    [rate]
  )
  const definition = props.entry.draft?.definition ?? props.entry.definition
  const [priceDirty, setPriceDirty] = useState(false)
  const [officialReferences, setOfficialReferences] = useState<
    OfficialReferenceConfiguration | undefined
  >(definition.official_references)
  const [referenceDirty, setReferenceDirty] = useState(false)
  const onDirtyChange = props.onDirtyChange
  const form = useForm<UnifiedModelFormValues>({
    resolver: zodResolver(unifiedModelFormSchema),
    defaultValues: {
      ...definition,
      parameter_json: definition.parameters
        ? JSON.stringify(definition.parameters, null, 2)
        : '',
      cost_json: JSON.stringify(definition.cost_rules ?? [], null, 2),
    },
  })
  const routeFields = useFieldArray({ control: form.control, name: 'routes' })
  const kind = form.watch('kind')
  const enabled = form.watch('enabled')
  const editor = useRef<ModelPricingEditorPanelHandle>(null)
  const pricing = useModelPricing([definition.model_name], true)
  const pricingEntry = pricing.data?.entries.find(
    (entry) => entry.model_name === definition.model_name
  )
  const activeProviders =
    pricingEntry?.plugin_variants?.filter((variant) => !variant.stale) ?? []
  const providerOnly =
    activeProviders.length > 0 &&
    activeProviders.every((variant) => Boolean(variant.configured))
  const initialPrice = useMemo(
    () => pricingRow(definition.model_name, definition.pricing),
    [definition]
  )
  useEffect(
    () => onDirtyChange(form.formState.isDirty || priceDirty || referenceDirty),
    [form.formState.isDirty, priceDirty, referenceDirty, onDirtyChange]
  )
  const persist = form.handleSubmit(async (values) => {
    form.clearErrors('root')
    try {
      if (
        containerRef.current?.querySelector(
          '[data-official-reference-invalid="true"]'
        )
      ) {
        throw new Error(
          t(
            'Complete the official price, public HTTPS source, and checked date before saving.'
          )
        )
      }
      if (
        containerRef.current?.querySelector('[data-generation-invalid="true"]')
      ) {
        throw new Error(
          t(
            'Correct invalid generation parameters before saving. Existing settings are preserved.'
          )
        )
      }
      if (
        containerRef.current?.querySelector(
          '[aria-invalid="true"], [data-billing-invalid="true"]'
        )
      ) {
        throw new Error(
          t(
            'Complete invalid price fields before saving. Existing prices are preserved.'
          )
        )
      }
      if (priceDirty) {
        const latest = await cache.fetchQuery({
          ...statusQueryOptions,
          staleTime: 0,
        })
        const currentRate = Number(latest?.usd_exchange_rate)
        if (
          !Number.isFinite(currentRate) ||
          currentRate <= 0 ||
          currentRate !== rate
        ) {
          throw new Error(
            t(
              'The exchange rate changed. Review the CNY prices before saving; existing prices are preserved.'
            )
          )
        }
      }
      const priceDraft = priceDirty ? await editor.current?.commitDraft() : null
      if (priceDirty && !priceDraft) return
      const parameters: unknown = values.parameter_json.trim()
        ? JSON.parse(values.parameter_json)
        : undefined
      const costs: unknown = JSON.parse(values.cost_json)
      if (
        (parameters !== undefined &&
          (!parameters ||
            typeof parameters !== 'object' ||
            Array.isArray(parameters))) ||
        !Array.isArray(costs)
      ) {
        throw new Error(
          t('Parameters must be an object and costs must be an array')
        )
      }
      let nextPricing = definition.pricing
      if (priceDraft) {
        nextPricing = pricingFromDraft(priceDraft)
        if (providerOnly) {
          nextPricing = {
            ...definition.pricing,
            'billing_setting.plugin_billing_expr':
              nextPricing['billing_setting.plugin_billing_expr'],
          }
        }
      }
      const next: UnifiedModelDefinition = {
        model_name: definition.model_name,
        display_name: values.display_name,
        enabled: values.enabled,
        kind: values.kind,
        routes: values.routes,
        source: values.source,
        pricing: nextPricing,
        cost_rules: costs as Record<string, unknown>[],
        official_references: officialReferences,
      }
      if (values.kind === 'text') {
        next.context_window = Number.isFinite(values.context_window)
          ? values.context_window
          : undefined
        next.max_output_tokens = Number.isFinite(values.max_output_tokens)
          ? values.max_output_tokens
          : undefined
        next.budget_field = values.budget_field || 'max_tokens'
      } else {
        next.parameters = parameters as Record<string, unknown> | undefined
      }
      await props.onSave(next)
    } catch (error) {
      form.setError('root', {
        message:
          error instanceof Error
            ? error.message
            : t('Failed to save model configuration'),
      })
    }
  })

  return (
    <div
      ref={containerRef}
      className='space-y-8 [&>section]:border-t [&>section]:pt-6'
      aria-busy={props.pending}
    >
      <fieldset disabled={props.pending} className='space-y-4'>
        <legend className='mb-3 text-base font-semibold'>
          {t('Model and availability')}
        </legend>
        <div className='grid gap-4 sm:grid-cols-2'>
          <div className='space-y-2'>
            <Label htmlFor='unified-display-name'>{t('Display name')}</Label>
            <Input
              id='unified-display-name'
              aria-required
              aria-invalid={Boolean(form.formState.errors.display_name)}
              {...form.register('display_name', {
                required: true,
                maxLength: 200,
              })}
            />
            <FieldError>
              {form.formState.errors.display_name?.message &&
                t(form.formState.errors.display_name.message)}
            </FieldError>
          </div>
          <div className='space-y-2'>
            <Label htmlFor='unified-kind'>{t('Unified model type')}</Label>
            <Controller
              control={form.control}
              name='kind'
              render={({ field }) => (
                <ComboboxInput
                  id='unified-kind'
                  value={field.value}
                  onValueChange={field.onChange}
                  options={[
                    { value: 'text', label: t('Text') },
                    { value: 'image', label: t('Image') },
                    { value: 'video', label: t('Video') },
                  ]}
                />
              )}
            />
          </div>
        </div>
        <Controller
          control={form.control}
          name='enabled'
          render={({ field }) => (
            <div className='flex items-center gap-2'>
              <Switch
                id='unified-enabled'
                checked={field.value}
                onCheckedChange={field.onChange}
              />
              <Label htmlFor='unified-enabled'>
                {t('Enable for new requests')}
              </Label>
            </div>
          )}
        />
        <div className='space-y-2'>
          <Label htmlFor='unified-source'>{t('Configuration source')}</Label>
          <Input
            id='unified-source'
            aria-required
            aria-invalid={Boolean(form.formState.errors.source)}
            {...form.register('source', { required: true })}
          />
          <FieldError>
            {form.formState.errors.source?.message &&
              t(form.formState.errors.source.message)}
          </FieldError>
        </div>
      </fieldset>
      <section className='space-y-3' aria-label={t('Upstream routes')}>
        <h3 className='font-semibold'>{t('Upstream routes')}</h3>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Existing channel credentials are reused. Disabled channels remain disabled.'
          )}
        </p>
        {routeFields.fields.map((route, index) => (
          <div
            key={route.id}
            className='grid items-end gap-3 border-b pb-4 sm:grid-cols-2'
          >
            <div className='space-y-2'>
              <Label htmlFor={`unified-channel-${index}`}>{t('Channel')}</Label>
              <Controller
                control={form.control}
                name={`routes.${index}.channel_id`}
                render={({ field }) => (
                  <ComboboxInput
                    id={`unified-channel-${index}`}
                    value={String(field.value)}
                    onValueChange={(value) => field.onChange(Number(value))}
                    options={props.channels.map((channel) => ({
                      value: String(channel.id),
                      label: channel.name,
                      description:
                        channel.status === 1 ? t('Enabled') : t('Disabled'),
                    }))}
                  />
                )}
              />
            </div>
            <div className='space-y-2'>
              <Label htmlFor={`unified-group-${index}`}>{t('Group')}</Label>
              <Input
                id={`unified-group-${index}`}
                {...form.register(`routes.${index}.group`, { required: true })}
              />
            </div>
            <div className='space-y-2'>
              <Label htmlFor={`unified-native-${index}`}>
                {t('Upstream model ID')}
              </Label>
              <Input
                id={`unified-native-${index}`}
                {...form.register(`routes.${index}.upstream_model`, {
                  required: true,
                })}
              />
            </div>
            <div className='flex items-center justify-between gap-3'>
              <Controller
                control={form.control}
                name={`routes.${index}.enabled`}
                render={({ field }) => (
                  <div className='flex items-center gap-2'>
                    <Switch
                      id={`unified-route-${index}`}
                      checked={field.value}
                      onCheckedChange={field.onChange}
                    />
                    <Label htmlFor={`unified-route-${index}`}>
                      {t('Enabled')}
                    </Label>
                  </div>
                )}
              />
              <Button variant='ghost' onClick={() => routeFields.remove(index)}>
                {t('Remove route')}
              </Button>
            </div>
            {kind === 'text' && (
              <div className='space-y-2 sm:col-span-2'>
                <Label htmlFor={`unified-protocol-${index}`}>
                  {t('Text route protocol')}
                </Label>
                <Controller
                  control={form.control}
                  name={`routes.${index}.text_protocol`}
                  render={({ field }) => (
                    <ComboboxInput
                      id={`unified-protocol-${index}`}
                      value={field.value ?? ''}
                      onValueChange={field.onChange}
                      options={[
                        {
                          value: '',
                          label: t('Preserve existing protocol selection'),
                        },
                        {
                          value: 'legacy',
                          label: t('Use original model-based protocol'),
                        },
                        { value: 'openai', label: t('OpenAI-compatible text') },
                        {
                          value: 'ycapi_fixed_lane',
                          label: t('YCAPI integrated fixed-lane text'),
                        },
                      ]}
                    />
                  )}
                />
              </div>
            )}
          </div>
        ))}
        <Button
          variant='outline'
          disabled={props.pending || !props.channels.length}
          onClick={() =>
            routeFields.append({
              channel_id: props.channels[0]?.id ?? 0,
              group: props.channels[0]?.groups[0] ?? 'default',
              upstream_model: definition.model_name,
              enabled: true,
            })
          }
        >
          {t('Add route')}
        </Button>
      </section>
      {kind === 'text' ? (
        <fieldset className='space-y-4' disabled={props.pending}>
          <legend className='mb-3 font-semibold'>
            {t('Context and output limits')}
          </legend>
          <p className='text-muted-foreground max-w-prose text-sm'>
            {t(
              'Use this model’s verified channel limits or an explicitly declared conservative platform limit. These values do not claim native maxima. Unknown limits remain blank.'
            )}
          </p>
          <div className='grid gap-4 sm:grid-cols-2'>
            <div className='space-y-2'>
              <Label htmlFor='unified-context'>{t('Context window')}</Label>
              <Input
                id='unified-context'
                aria-required={enabled}
                aria-invalid={Boolean(form.formState.errors.context_window)}
                type='number'
                min={1}
                {...form.register('context_window', {
                  valueAsNumber: true,
                  required: enabled,
                  min: 1,
                })}
              />
              <FieldError>
                {form.formState.errors.context_window?.message &&
                  t(form.formState.errors.context_window.message)}
              </FieldError>
            </div>
            <div className='space-y-2'>
              <Label htmlFor='unified-output'>
                {t('Maximum output tokens')}
              </Label>
              <Input
                id='unified-output'
                aria-required={enabled}
                aria-invalid={Boolean(form.formState.errors.max_output_tokens)}
                type='number'
                min={1}
                {...form.register('max_output_tokens', {
                  valueAsNumber: true,
                  required: enabled,
                  min: 1,
                })}
              />
              <FieldError>
                {form.formState.errors.max_output_tokens?.message &&
                  t(form.formState.errors.max_output_tokens.message)}
              </FieldError>
            </div>
            <div className='space-y-2 sm:col-span-2'>
              <Label htmlFor='unified-budget'>{t('Output budget field')}</Label>
              <Controller
                control={form.control}
                name='budget_field'
                render={({ field }) => (
                  <ComboboxInput
                    id='unified-budget'
                    value={field.value}
                    onValueChange={field.onChange}
                    options={[
                      'max_tokens',
                      'max_completion_tokens',
                      'max_output_tokens',
                      'generationConfig.maxOutputTokens',
                    ].map((value) => ({ value, label: value }))}
                  />
                )}
              />
            </div>
          </div>
        </fieldset>
      ) : (
        <section
          className='space-y-3'
          aria-label={t('Image and video parameters')}
        >
          <h3 className='font-semibold'>{t('Image and video parameters')}</h3>
          <p className='text-muted-foreground max-w-prose text-sm'>
            {t(
              'Set image and video options below. Published changes apply to future requests; existing tasks keep their original parameters.'
            )}
          </p>
          <Controller
            control={form.control}
            name='parameter_json'
            render={({ field }) => (
              <MediaParametersForm
                key={definition.model_name}
                value={field.value}
                nativeSchema={props.entry.legacy_parameters}
                disabled={props.pending}
                onChange={field.onChange}
              />
            )}
          />
        </section>
      )}
      <section className='space-y-3' aria-label={t('User pricing')}>
        <h3 className='font-semibold'>{t('User pricing')}</h3>
        {pricing.isPending || status.isPending ? <LoadingState /> : null}
        {pricing.isError && (
          <ErrorState
            title={t('Failed to load model pricing')}
            onRetry={() => void pricing.refetch()}
          />
        )}
        {!pricing.isPending && !pricing.isError && !status.isPending && (
          <ModelPricingEditorPanel
            ref={editor}
            embedded
            currencyOverride={cnyCurrency}
            tableOnly
            editData={initialPrice}
            usageSchema={pricingEntry?.usage_schema}
            pluginVariants={pricingEntry?.plugin_variants}
            initialProvider={
              pricingEntry?.plugin_variants?.find(
                (variant) => !variant.stale && Boolean(variant.configured)
              )?.plugin_key
            }
            providerOnly={providerOnly}
            isSaving={props.pending}
            onDirtyChange={setPriceDirty}
            className='border-0'
          />
        )}
      </section>
      <OfficialReferencesForm
        value={officialReferences}
        specifications={props.entry.reference_specifications ?? []}
        modelName={definition.model_name}
        disabled={props.pending}
        onChange={(value) => {
          setOfficialReferences(value)
          setReferenceDirty(true)
        }}
      />
      <SettingsSection title={t('Procurement costs in CNY')}>
        <p className='text-muted-foreground text-sm'>
          {t(
            'Cost changes append a new effective record. Existing task and financial history are preserved.'
          )}
        </p>
        <Controller
          control={form.control}
          name='cost_json'
          render={({ field }) => (
            <CostRulesTable
              modelName={definition.model_name}
              channelId={form.watch('routes')[0]?.channel_id}
              value={field.value}
              onChange={field.onChange}
              disabled={props.pending}
            />
          )}
        />
      </SettingsSection>
      {form.formState.errors.root?.message && (
        <p role='alert' className='text-destructive text-sm'>
          {form.formState.errors.root.message}
        </p>
      )}
      {Object.keys(form.formState.errors).some((key) => key !== 'root') && (
        <p role='alert' className='text-destructive text-sm'>
          {t('Complete the required model fields before saving')}
        </p>
      )}
      <div className='flex flex-wrap items-center gap-3 border-t pt-4'>
        <Button disabled={props.pending} onClick={() => void persist()}>
          {t('Validate and save draft')}
        </Button>
        <p className='text-muted-foreground text-sm'>
          {t('Drafts do not change the current service.')}
        </p>
      </div>
    </div>
  )
}
