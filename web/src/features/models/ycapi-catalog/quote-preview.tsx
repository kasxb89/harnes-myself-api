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
import { useMutation } from '@tanstack/react-query'
import { useRef, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'

import { CodeBlock } from '@/components/ai-elements/code-block'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Textarea } from '@/components/ui/textarea'
import { toIntlLocale } from '@/i18n/languages'
import { api } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'

import { formatCatalogCny } from './price-format'
import type { QuotePreview, YcapiModel } from './types'

const schema = z.object({
  specId: z.string(),
  prompt: z.string().min(1).max(16000),
  inputTokens: z.number().int().min(0).max(32000),
  outputTokens: z.number().int().min(0).max(1000000),
  maxOutputTokens: z.number().int(),
  voice: z.string(),
  responseFormat: z.string(),
})
type Values = z.infer<typeof schema>
type Field = {
  default?: string
  enum?: string[]
  options?: { label: string; value: string }[]
}
export function QuotePreviewForm(props: { model: YcapiModel }) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const m = props.model
  const fields = m.paramSchema as Record<string, Field>
  const token = m.kind === 'text' || m.kind === 'audio'
  const form = useForm<Values>({
    resolver: zodResolver(schema),
    defaultValues: {
      specId: m.retailOffers[0].id,
      prompt: t('A white ceramic cup on a table.'),
      inputTokens: 1000,
      outputTokens: 1024,
      maxOutputTokens: 4096,
      voice: fields.voice?.default || '',
      responseFormat: fields.response_format?.default || '',
    },
  })
  const [quote, setQuote] = useState<QuotePreview | null>(null)
  const sequence = useRef(0)
  const mutation = useMutation({
    mutationFn: async (values: Values) => {
      const current = ++sequence.current
      const response = await api.post('/api/ycapi/quote-preview', {
        modelId: m.id,
        ...values,
      })
      const data = requireServerSuccess(response.data).data as QuotePreview
      if (current === sequence.current) setQuote(data)
      return data
    },
  })
  const optionLabel = (id: string) =>
    fields.retail_spec?.options?.find((o) => o.value === id)?.label || id
  return (
    <section className='rounded-lg border p-4' aria-label={t('Quote preview')}>
      <h3 className='mb-4 font-medium'>{t('Quote preview')}</h3>
      <form
        onSubmit={form.handleSubmit((values) => mutation.mutate(values))}
        onChange={() => {
          sequence.current++
          setQuote(null)
        }}
        className='space-y-4'
      >
        <div className='grid gap-4 sm:grid-cols-2'>
          {!token && (
            <div className='space-y-2'>
              <Label htmlFor='ycapi-spec'>{t('Specification')}</Label>
              <NativeSelect id='ycapi-spec' {...form.register('specId')}>
                {m.retailOffers.map((o) => (
                  <NativeSelectOption key={o.id} value={o.id}>
                    {optionLabel(o.id)}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </div>
          )}
          {token && (
            <>
              <div className='space-y-2'>
                <Label htmlFor='ycapi-input'>{t('Example input tokens')}</Label>
                <Input
                  id='ycapi-input'
                  type='number'
                  min={0}
                  max={32000}
                  {...form.register('inputTokens', { valueAsNumber: true })}
                />
              </div>
              <div className='space-y-2'>
                <Label htmlFor='ycapi-output'>
                  {t('Example output tokens')}
                </Label>
                <Input
                  id='ycapi-output'
                  type='number'
                  min={0}
                  max={m.kind === 'text' ? 8192 : 1000000}
                  {...form.register('outputTokens', { valueAsNumber: true })}
                />
              </div>
            </>
          )}
          {m.kind === 'text' && (
            <div className='space-y-2'>
              <Label htmlFor='ycapi-limit'>{t('Maximum output tokens')}</Label>
              <NativeSelect
                id='ycapi-limit'
                {...form.register('maxOutputTokens', { valueAsNumber: true })}
              >
                {[1024, 2048, 4096, 8192].map((n) => (
                  <NativeSelectOption key={n} value={n}>
                    {n}
                  </NativeSelectOption>
                ))}
              </NativeSelect>
            </div>
          )}
          {m.kind === 'audio' &&
            (['voice', 'response_format'] as const).map((key) => (
              <div key={key} className='space-y-2'>
                <Label htmlFor={`ycapi-${key}`}>
                  {key === 'voice' ? t('Voice') : t('Format')}
                </Label>
                <NativeSelect
                  id={`ycapi-${key}`}
                  {...form.register(
                    key === 'voice' ? 'voice' : 'responseFormat'
                  )}
                >
                  {fields[key]?.enum?.map((v) => (
                    <NativeSelectOption key={v} value={v}>
                      {v}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              </div>
            ))}
        </div>
        <div className='space-y-2'>
          <Label htmlFor='ycapi-prompt'>{t('Prompt')}</Label>
          <Textarea
            id='ycapi-prompt'
            rows={2}
            maxLength={m.kind === 'audio' ? 2000 : 16000}
            {...form.register('prompt')}
          />
        </div>
        {Object.keys(form.formState.errors).length > 0 && (
          <p role='alert' className='text-destructive text-sm'>
            {t('Check the prompt and token budget.')}
          </p>
        )}
        <Button type='submit' variant='outline' disabled={mutation.isPending}>
          {mutation.isPending ? t('Calculating...') : t('Calculate preview')}
        </Button>
      </form>
      {quote && (
        <div className='mt-4 space-y-4'>
          <div className='bg-muted grid gap-3 rounded-lg p-3 sm:grid-cols-3'>
            <div>
              <p className='text-muted-foreground text-xs'>
                {t('Procurement price')}
              </p>
              <strong>
                {formatCatalogCny(Number(quote.procurementCny), locale)}
              </strong>
            </div>
            <div>
              <p className='text-muted-foreground text-xs'>
                {t('Retail price')}
              </p>
              <strong>
                {formatCatalogCny(Number(quote.retailCny), locale)}
              </strong>
            </div>
            <div>
              <p className='text-muted-foreground text-xs'>
                {t('Official benchmark')}
              </p>
              <strong>
                {formatCatalogCny(Number(quote.officialCny), locale)}
              </strong>
            </div>
          </div>
          <Alert>
            <AlertDescription>
              {token
                ? t(
                    'Token amounts are examples, not a final bill or a live upstream quote.'
                  )
                : t(
                    'The price covers the complete selected native specification.'
                  )}{' '}
              {t('No funds reserved. Generation is disabled in this preview.')}
            </AlertDescription>
          </Alert>
          <CodeBlock
            code={JSON.stringify(
              { endpoint: quote.endpoint, body: quote.upstreamBody },
              null,
              2
            )}
            language='json'
            title={t('Request preview')}
            maxExpandedLines={10}
            enableCollapse
          />
        </div>
      )}
    </section>
  )
}
