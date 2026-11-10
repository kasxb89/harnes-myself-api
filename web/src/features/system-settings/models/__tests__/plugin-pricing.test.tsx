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
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createRef } from 'react'
import { afterEach, expect, it, vi } from 'vitest'

import { UnifiedModelForm } from '@/features/client-model-config/components/unified-model-form'
import type { UnifiedModelDefinition } from '@/features/client-model-config/unified-api'
import type {
  ModelPricingConfig,
  ModelPricingPluginVariant,
} from '@/features/model-pricing/api'
import { USD_PRICING_CURRENCY } from '@/features/model-pricing/currency'
import { ModelPricingPanel } from '@/features/model-pricing/model-pricing-panel'
import { pricingOptions } from '@/features/model-pricing/pricing'
import { evaluateBillingExpression } from '@/features/pricing/lib/billing-expression/runtime'
import { api } from '@/lib/api'
import { useAuthStore } from '@/stores/auth-store'
import { usePricingPreferencesStore } from '@/stores/pricing-preferences-store'
import { useSystemConfigStore } from '@/stores/system-config-store'

import {
  ModelPricingEditorPanel,
  type ModelPricingEditorPanelHandle,
  type ModelRatioData,
} from '../model-pricing-sheet'
import { TaskPluginPricingEditor } from '../task-plugin-pricing-editor'

let client: QueryClient | undefined

it('loads the configured provider price and preserves its exact bytes when only model metadata changes', async () => {
  useAuthStore.getState().auth.setUser({ id: 1, username: 'root', role: 100 })
  let usdRate = 7
  const expression = 'tier("video", u("seconds") * 0.0410959)'
  const pricing = {
    'billing_setting.plugin_billing_expr': { hailuo: expression },
    ModelRatio: 0,
  }
  const usage = {
    seconds: {
      type: 'number' as const,
      unit: 'second',
      description: 'Video unit price',
    },
  }
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/status') {
      return { data: { success: true, data: { usd_exchange_rate: usdRate } } }
    }
    return {
      data: {
        success: true,
        data:
          url === '/api/option/model_pricing'
            ? {
                entries: [
                  {
                    model_name: 'MiniMax-H3',
                    version: 'original-price',
                    configured: pricing,
                    effective: pricing,
                    usage_schema: usage,
                    plugin_variants: [
                      {
                        plugin_key: 'hailuo',
                        plugin_name: 'Hailuo Video',
                        configured: expression,
                        effective: expression,
                        compatible: true,
                        usage_schema: usage,
                      },
                    ],
                  },
                ],
                options: {},
                empty_version: 'empty',
              }
            : [],
        vendors: [],
      },
    }
  })
  vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, data: { effective: {} } },
  })
  const definition: UnifiedModelDefinition = {
    model_name: 'MiniMax-H3',
    kind: 'video',
    enabled: true,
    display_name: 'Original H3',
    source: 'Verified fixture price source',
    routes: [
      {
        channel_id: 790,
        group: 'default',
        upstream_model: 'MiniMax-H3',
        enabled: true,
      },
    ],
    parameters: {
      type: 'object',
      properties: {
        seconds: { type: 'integer', minimum: 4, maximum: 30, default: 5 },
      },
    },
    pricing,
  }
  const save = vi.fn().mockResolvedValue(undefined)
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  render(
    <QueryClientProvider client={client}>
      <UnifiedModelForm
        entry={{
          model_name: 'MiniMax-H3',
          base_version: 'original',
          definition,
        }}
        channels={[
          {
            id: 790,
            name: 'Existing H3',
            status: 1,
            type: 35,
            groups: ['default'],
          },
        ]}
        pending={false}
        onSave={save}
        onDirtyChange={() => undefined}
      />
    </QueryClientProvider>
  )
  expect(
    await screen.findByRole('tab', { name: 'Hailuo Video' })
  ).toHaveAttribute('aria-selected', 'true')
  expect(
    screen.queryByRole('textbox', { name: 'Generation parameter schema' })
  ).toBeNull()
  expect(screen.getByText(/Chinese yuan/)).toBeVisible()
  expect(screen.getByRole('table', { name: 'CNY prices' })).toBeVisible()
  expect(screen.queryByRole('combobox', { name: 'Editor mode' })).toBeNull()
  expect(
    screen.queryByRole('combobox', { name: 'Pricing currency' })
  ).toBeNull()
  expect(screen.queryByRole('tab', { name: 'Default' })).toBeNull()
  expect(screen.queryByRole('complementary', { name: 'Preview' })).toBeNull()
  const user = userEvent.setup()
  await user.clear(screen.getByRole('textbox', { name: 'Display name' }))
  await user.type(
    screen.getByRole('textbox', { name: 'Display name' }),
    'Updated H3 label'
  )
  fireEvent.change(
    within(
      screen.getByRole('group', { name: 'General generation parameters' })
    ).getByRole('textbox', { name: 'Maximum for Video duration (seconds)' }),
    { target: { value: '20' } }
  )
  await user.click(
    screen.getByRole('button', { name: 'Validate and save draft' })
  )
  await waitFor(() => expect(save).toHaveBeenCalledTimes(1))
  expect(save.mock.calls[0][0].parameters).toEqual({
    type: 'object',
    properties: {
      seconds: { type: 'integer', minimum: 4, maximum: 20, default: 5 },
    },
  })
  expect(save.mock.calls[0][0].pricing).toEqual(pricing)
  expect(save.mock.calls[0][0].pricing).not.toHaveProperty(
    'billing_setting.billing_expr'
  )
  expect(save.mock.calls[0][0].pricing).not.toHaveProperty('ModelPrice')
  const price = within(
    screen.getByRole('table', { name: 'CNY prices' })
  ).getByRole('textbox')
  fireEvent.change(price, { target: { value: '' } })
  await user.click(
    screen.getByRole('button', { name: 'Validate and save draft' })
  )
  expect(save).toHaveBeenCalledTimes(1)
  expect(
    screen.getByText(
      'Complete invalid price fields before saving. Existing prices are preserved.'
    )
  ).toBeVisible()
  fireEvent.change(price, { target: { value: '0.5' } })
  await user.click(
    screen.getByRole('button', { name: 'Validate and save draft' })
  )
  await waitFor(() => expect(save).toHaveBeenCalledTimes(2))
  const changed =
    save.mock.calls[1][0].pricing['billing_setting.plugin_billing_expr'].hailuo
  const result = evaluateBillingExpression(changed, { usage: { seconds: 5 } })
  expect(result.status).toBe('success')
  if (result.status === 'success') {
    expect(
      result.cost *
        useSystemConfigStore.getState().config.currency.usdExchangeRate
    ).toBeCloseTo(2.5, 10)
  }
  usdRate = 8
  fireEvent.change(
    within(screen.getByRole('table', { name: 'CNY prices' })).getByRole(
      'textbox'
    ),
    {
      target: { value: '0.9' },
    }
  )
  await user.click(
    screen.getByRole('button', { name: 'Validate and save draft' })
  )
  expect(
    await screen.findByText(
      'The exchange rate changed. Review the CNY prices before saving; existing prices are preserved.'
    )
  ).toBeVisible()
  expect(save).toHaveBeenCalledTimes(2)
})

afterEach(() => {
  client?.clear()
  useAuthStore.getState().auth.setUser(null)
  localStorage.clear()
  vi.restoreAllMocks()
})

it('keeps provider drafts across tabs and saves nested expressions with the model version', async () => {
  useAuthStore
    .getState()
    .auth.setUser({ id: 1, username: 'administrator', role: 100 })
  usePricingPreferencesStore.setState({ currency: 'USD' })
  const expression = 'tier("base", u("seconds") * 0.4)'
  const values = {
    'billing_setting.billing_mode': 'tiered_expr',
    'billing_setting.billing_expr': expression,
  }
  const snapshot: ModelPricingConfig = {
    entries: [
      {
        model_name: 'shared',
        version: 'version-with-providers',
        configured: values,
        effective: values,
        usage_schema: {
          seconds: {
            type: 'number',
            unit: 'second',
            description: 'Video unit price',
          },
        },
        plugin_variants: [
          {
            plugin_key: 'alpha',
            plugin_name: 'Alpha',
            usage_schema: {
              seconds: {
                type: 'number',
                unit: 'second',
                description: 'Video unit price',
              },
            },
            configured: '',
            effective: expression,
            compatible: true,
          },
          {
            plugin_key: 'beta',
            plugin_name: 'Beta',
            usage_schema: {
              credits: {
                type: 'number',
                unit: 'credit',
                description: 'Credit unit price',
              },
            },
            configured: '',
            effective: expression,
            compatible: false,
          },
        ],
      },
    ],
    options: pricingOptions({}),
    empty_version: 'empty',
  }
  vi.spyOn(api, 'get').mockImplementation(async (url) => {
    if (url === '/api/option/model_pricing') {
      return { data: { success: true, data: snapshot } }
    }
    if (url === '/api/pricing') {
      return { data: { success: true, data: [], vendors: [] } }
    }
    return { data: { success: true, data: {} } }
  })
  let finishSave: (() => void) | undefined
  const save = vi.spyOn(api, 'patch').mockImplementation(
    () =>
      new Promise((resolve) => {
        finishSave = () => resolve({ data: { success: true } })
      })
  )
  const dirty = vi.fn()
  client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  render(
    <QueryClientProvider client={client}>
      <ModelPricingPanel modelName='shared' onDirtyChange={dirty} />
    </QueryClientProvider>
  )
  const user = userEvent.setup()
  const defaultTab = await screen.findByRole('tab', { name: 'Default' })
  expect(defaultTab).toHaveAttribute('aria-selected', 'true')
  expect(
    await screen.findByText(
      'The model-level expression cannot be evaluated by: Beta'
    )
  ).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Beta' }))
  const betaPanel = screen.getByRole('tabpanel', { name: 'Beta' })
  expect(
    within(betaPanel).getByText('Not configured for this provider')
  ).toBeVisible()
  const separate = within(betaPanel).getByRole('switch', {
    name: 'Set separately for this provider',
  })
  expect(separate).not.toBeChecked()
  await user.click(separate)
  expect(separate).toBeChecked()
  await user.click(separate)
  expect(separate).not.toBeChecked()
  expect(
    within(betaPanel).getByText('Use model-level expression')
  ).toBeVisible()
  expect(
    within(betaPanel).queryByRole('textbox', { name: 'credits' })
  ).not.toBeInTheDocument()
  await user.click(separate)
  const price = within(betaPanel).getByRole('textbox', { name: 'credits' })
  await user.clear(price)
  await user.type(price, '1.5')
  expect(dirty).toHaveBeenLastCalledWith(true)
  await user.click(defaultTab)
  expect(
    screen.queryByText(
      'The model-level expression cannot be evaluated by: Beta'
    )
  ).not.toBeInTheDocument()
  await user.click(screen.getByRole('tab', { name: 'Beta' }))
  expect(price).toHaveValue('1.5')
  const saveButton = screen.getByRole('button', { name: 'Save model prices' })
  await user.click(saveButton)
  await waitFor(() => expect(save).toHaveBeenCalledOnce())
  expect(saveButton).toBeDisabled()
  const changes = save.mock.calls[0][1] as {
    changes: { expected_version: string; pricing: Record<string, unknown> }[]
  }
  expect(changes.changes[0].expected_version).toBe('version-with-providers')
  expect(changes.changes[0].pricing['billing_setting.billing_expr']).toBe(
    expression
  )
  expect(
    changes.changes[0].pricing['billing_setting.plugin_billing_expr']
  ).toEqual({ beta: 'tier("base", u("credits") * 1.5)' })
  await act(async () => {
    finishSave?.()
  })
  await waitFor(() => expect(saveButton).toBeEnabled())
})

it('keeps long provider names accessible while allowing the tabs to wrap', () => {
  const longName =
    'Provider with a long international service name and a regional deployment designation'
  render(
    <TaskPluginPricingEditor
      variants={[
        {
          plugin_key: 'alpha',
          plugin_name: 'Alpha',
          usage_schema: {},
          configured: '',
          effective: '1',
          compatible: true,
        },
        {
          plugin_key: 'beta',
          plugin_name: longName,
          usage_schema: {},
          configured: '',
          effective: '1',
          compatible: true,
        },
      ]}
      expressions={{}}
      onChange={vi.fn()}
      modelExpression='1'
      currency={USD_PRICING_CURRENCY}
    >
      <span>Default editor</span>
    </TaskPluginPricingEditor>
  )
  expect(screen.getByRole('tablist', { name: 'Provider' })).toHaveClass(
    'flex-wrap'
  )
  expect(screen.getByRole('tab', { name: longName })).toHaveClass(
    'max-w-full',
    'min-w-0'
  )
  expect(screen.getByText(longName)).toHaveAttribute('title', longName)
})

it('lets an administrator remove an unavailable provider’s saved price', async () => {
  const expression = 'u("images")'
  const { ref } = renderEditor(
    {
      name: 'shared',
      billingMode: 'tiered_expr',
      billingExpr: expression,
      pluginBillingExpr: { retired: expression },
    },
    [
      {
        plugin_key: 'retired',
        plugin_name: 'Retired',
        stale: true,
        usage_schema: {},
        configured: expression,
        effective: '',
        compatible: false,
      },
    ]
  )
  const user = userEvent.setup()
  await user.click(screen.getByRole('tab', { name: 'Retired' }))
  expect(
    screen.getByText('This provider is unavailable for this model.')
  ).toBeVisible()
  await user.click(screen.getByRole('button', { name: 'Remove saved price' }))
  expect(await ref.current?.commitDraft()).toMatchObject({
    pluginBillingExpr: {},
    billingExpr: expression,
  })
  expect(screen.getByRole('tab', { name: 'Default' })).toHaveAttribute(
    'aria-selected',
    'true'
  )
})

it('shows pending server validation for an expression the browser cannot check', async () => {
  render(
    <TaskPluginPricingEditor
      variants={[
        {
          plugin_key: 'alpha',
          plugin_name: 'Alpha',
          usage_schema: {},
          configured: '',
          effective: '1',
          compatible: true,
        },
      ]}
      expressions={{}}
      onChange={vi.fn()}
      modelExpression='futureFunction()'
      currency={USD_PRICING_CURRENCY}
    >
      <span>Default editor</span>
    </TaskPluginPricingEditor>
  )
  await userEvent.setup().click(screen.getByRole('tab', { name: 'Alpha' }))
  expect(
    screen.getByText('Expression compatibility will be checked when saving.')
  ).toBeVisible()
})

function renderEditor(
  editData: ModelRatioData,
  variants: ModelPricingPluginVariant[],
  dirty = vi.fn()
) {
  usePricingPreferencesStore.setState({ currency: 'USD' })
  vi.spyOn(api, 'get').mockResolvedValue({
    data: { success: true, data: [], vendors: [] },
  })
  vi.spyOn(api, 'post').mockResolvedValue({
    data: { success: true, data: { effective: {} } },
  })
  client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const ref = createRef<ModelPricingEditorPanelHandle>()
  render(
    <QueryClientProvider client={client}>
      <ModelPricingEditorPanel
        ref={ref}
        embedded
        editData={editData}
        pluginVariants={variants}
        onDirtyChange={dirty}
      />
    </QueryClientProvider>
  )
  return { ref, dirty }
}

it('shows and removes a single provider override while retaining the per-call default', async () => {
  const expression = 'tier("base", u("seconds") * 0.4)'
  const { ref } = renderEditor(
    {
      name: 'shared',
      billingMode: 'per-request',
      price: '0.25',
      pluginBillingExpr: { alpha: expression },
    },
    [
      {
        plugin_key: 'alpha',
        plugin_name: 'Alpha',
        usage_schema: { seconds: { type: 'number', unit: 'second' } },
        configured: expression,
        effective: expression,
        compatible: true,
      },
    ]
  )
  expect(
    screen.getByRole('tab', { name: 'Per-request (deprecated)' })
  ).toHaveAttribute('aria-selected', 'true')
  const user = userEvent.setup()
  await user.click(screen.getByRole('tab', { name: 'Alpha' }))
  await user.click(
    screen.getByRole('switch', { name: 'Set separately for this provider' })
  )
  expect(screen.getByText('Use model-level pricing')).toBeVisible()
  expect(await ref.current?.commitDraft()).toMatchObject({
    billingMode: 'per-request',
    price: '0.25',
    pluginBillingExpr: {},
  })
})

it('clears dirty state after restoring an override in a different key order', async () => {
  const expression = 'tier("base", u("seconds") * 0.4)'
  const { dirty } = renderEditor(
    {
      name: 'shared',
      billingMode: 'tiered_expr',
      billingExpr: expression,
      pluginBillingExpr: { alpha: expression, beta: expression },
    },
    ['alpha', 'beta'].map((key) => ({
      plugin_key: key,
      plugin_name: key,
      usage_schema: { seconds: { type: 'number', unit: 'second' } },
      configured: expression,
      effective: expression,
      compatible: true,
    }))
  )
  const user = userEvent.setup()
  await user.click(screen.getByRole('tab', { name: 'alpha' }))
  const toggle = screen.getByRole('switch', {
    name: 'Set separately for this provider',
  })
  await user.click(toggle)
  expect(dirty).toHaveBeenLastCalledWith(true)
  await user.click(toggle)
  expect(dirty).toHaveBeenLastCalledWith(false)
})

it('opens task expression pricing when only provider metadata supplies a schema', async () => {
  renderEditor(
    { name: 'shared', billingMode: 'per-token' },
    ['alpha', 'beta'].map((key) => ({
      plugin_key: key,
      plugin_name: key,
      usage_schema: { seconds: { type: 'number', unit: 'second' } },
      configured: '',
      effective: '',
      compatible: false,
    }))
  )
  await waitFor(() =>
    expect(screen.getByRole('tab', { name: 'Expression' })).toHaveAttribute(
      'aria-selected',
      'true'
    )
  )
  expect(screen.getByRole('textbox', { name: 'seconds' })).toBeVisible()
})
