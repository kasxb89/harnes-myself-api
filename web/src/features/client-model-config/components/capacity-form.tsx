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
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { isAxiosError } from 'axios'
import { useEffect, useState } from 'react'
import { useForm } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { FormDirtyIndicator } from '@/features/system-settings/components/form-dirty-indicator'
import { SettingsPageFormActions } from '@/features/system-settings/components/settings-page-context'
import { handleServerError } from '@/lib/handle-server-error'

import { saveClientModelCapacities } from '../api'
import {
  capacitySchemaForRoutes,
  capacityRoutesToForm,
  type CapacityFormValues,
} from '../lib/capacity'
import type {
  ClientModelCapacityCatalog,
  ClientModelCapacityRoute,
} from '../types'

type CapacityFormProps = {
  active: boolean
  catalog: ClientModelCapacityCatalog
  model: string
  channelId: number
  routeUnavailable?: boolean
  onDirtyChange: (dirty: boolean) => void
  onReload: () => void
}

export function ClientModelCapacityForm(props: CapacityFormProps) {
  const { t } = useTranslation()
  const queryClient = useQueryClient()
  const [baseline, setBaseline] = useState(props.catalog)
  const [conflict, setConflict] = useState(false)
  const routes = baseline.routes.filter(
    (row) => row.model === props.model && row.channel_id === props.channelId
  )
  const form = useForm<CapacityFormValues>({
    resolver: zodResolver(capacitySchemaForRoutes(routes)),
    defaultValues: capacityRoutesToForm(routes),
  })
  const isDirty = form.formState.isDirty
  const save = useMutation({
    mutationFn: saveClientModelCapacities,
    onSuccess: (data) => {
      setBaseline(data)
      const updated = data.routes.filter(
        (row) => row.model === props.model && row.channel_id === props.channelId
      )
      form.reset(capacityRoutesToForm(updated))
      setConflict(false)
      queryClient.setQueryData(['client-model-capacities'], data)
      void queryClient.invalidateQueries({
        queryKey: ['client-model-config', 'models'],
      })
      void queryClient.invalidateQueries({ queryKey: ['pricing'] })
      toast.success(t('Model capacities saved'))
    },
    onError: (error) => {
      if (isAxiosError(error) && error.response?.status === 409) {
        setConflict(true)
      } else {
        handleServerError(error)
      }
    },
  })
  const notifyDirty = props.onDirtyChange
  useEffect(() => {
    notifyDirty(isDirty || save.isPending)
  }, [isDirty, save.isPending, notifyDirty])
  const reset = form.reset
  useEffect(() => {
    if (!isDirty && !save.isPending) {
      setBaseline(props.catalog)
      reset(
        capacityRoutesToForm(
          props.catalog.routes.filter(
            (row) =>
              row.model === props.model && row.channel_id === props.channelId
          )
        )
      )
    }
  }, [
    props.catalog,
    props.model,
    props.channelId,
    isDirty,
    reset,
    save.isPending,
  ])

  const onSubmit = (values: CapacityFormValues): void => {
    if (
      save.isPending ||
      conflict ||
      props.routeUnavailable ||
      routes.length === 0
    ) {
      return
    }
    const changes = routes.map((route: ClientModelCapacityRoute) => ({
      model: route.model,
      group: route.group,
      channel_id: route.channel_id,
      route_model: route.route_model,
      budget_field: route.budget_field,
      ...(values.context_window && {
        context_window: Number(values.context_window),
      }),
      ...(values.max_output_tokens && {
        max_output_tokens: Number(values.max_output_tokens),
      }),
      source: values.source,
      verified_at: values.verified_at,
    }))
    save.mutate({ revision: baseline.revision, routes: changes })
  }
  const invalidEvidence = routes.some((row) => row.configured && !row.valid)
  return (
    <Form {...form}>
      <form
        noValidate
        onSubmit={form.handleSubmit(onSubmit)}
        className='space-y-4'
      >
        <FormDirtyIndicator isDirty={isDirty} />
        {props.active && (
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            onReset={() => {
              form.reset()
              setConflict(false)
            }}
            resetLabel='Discard changes'
            saveLabel='Save model capacities'
            isSaving={save.isPending}
            isSaveDisabled={
              !isDirty ||
              conflict ||
              props.routeUnavailable ||
              routes.length === 0
            }
            isResetDisabled={!isDirty || save.isPending}
          />
        )}
        {(conflict || props.routeUnavailable) && (
          <Alert variant='destructive'>
            <AlertDescription>
              {t(
                props.routeUnavailable
                  ? 'This model route is unavailable. Discard changes and select an available route.'
                  : 'Model routes or capacities changed. Reload and review before saving.'
              )}
              <Button
                type='button'
                size='sm'
                variant='outline'
                onClick={() => {
                  form.reset()
                  setConflict(false)
                  props.onReload()
                }}
              >
                {t('Reload configuration')}
              </Button>
            </AlertDescription>
          </Alert>
        )}
        {invalidEvidence && (
          <Alert variant='destructive'>
            <AlertDescription>
              {t(
                'This channel changed since its limits were verified. Check the upstream specification before saving.'
              )}
            </AlertDescription>
          </Alert>
        )}
        <p className='text-muted-foreground text-sm break-words'>
          {t('Applies to groups')} {routes.map((row) => row.group).join(', ')}
        </p>
        <div className='grid min-w-0 gap-4 sm:grid-cols-2'>
          {(['context_window', 'max_output_tokens'] as const).map((name) => (
            <FormField
              key={name}
              control={form.control}
              name={name}
              render={({ field }) => (
                <FormItem>
                  <FormLabel>
                    {t(
                      name === 'context_window'
                        ? 'Context window (tokens)'
                        : 'Maximum output tokens'
                    )}
                  </FormLabel>
                  <FormControl>
                    <Input
                      {...field}
                      inputMode='numeric'
                      disabled={save.isPending}
                      placeholder={t('Whole token count')}
                      className='tabular-nums'
                    />
                  </FormControl>
                  <FormMessage />
                </FormItem>
              )}
            />
          ))}
        </div>
        <p className='text-muted-foreground text-sm'>
          {t(
            'The context window includes input and output. Set limits supported by this channel. When routes differ, the client receives the smallest verified limit.'
          )}{' '}
          {t(
            routes.some((row) => row.requires_full_capacity)
              ? 'Both token limits are required for this model'
              : 'Leave a token field blank to keep its saved value, or leave it unconfigured if none was verified.'
          )}
        </p>
        <div className='grid min-w-0 gap-4 sm:grid-cols-[minmax(0,1fr)_12rem]'>
          <FormField
            control={form.control}
            name='source'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Upstream specification source')}</FormLabel>
                <FormControl>
                  <Input
                    {...field}
                    disabled={save.isPending}
                    placeholder={t(
                      'Documentation URL or supplier confirmation'
                    )}
                  />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
          <FormField
            control={form.control}
            name='verified_at'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Verification date')}</FormLabel>
                <FormControl>
                  <Input {...field} type='date' disabled={save.isPending} />
                </FormControl>
                <FormMessage />
              </FormItem>
            )}
          />
        </div>
      </form>
    </Form>
  )
}
