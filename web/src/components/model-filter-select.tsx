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
import { useQuery } from '@tanstack/react-query'
import type { AriaAttributes } from 'react'
import { useTranslation } from 'react-i18next'

import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { Combobox } from '@/components/ui/combobox'
import { getUserModels } from '@/lib/api'
import { requireServerSuccess } from '@/lib/server-error-message'
import { useAuthStore } from '@/stores/auth-store'

interface ModelFilterSelectProps {
  value: string
  models?: readonly string[]
  onValueChange: (value: string) => void
  disabled?: boolean
  id?: string
  className?: string
  popupClassName?: string
  'aria-label'?: string
  'aria-invalid'?: AriaAttributes['aria-invalid']
}

// Historical report names and URL selections remain searchable even when a
// channel is retired. The catalog itself uses only the current user's models.
export function ModelFilterSelect(props: ModelFilterSelectProps) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const sid = useAuthStore((state) => state.auth.session?.sid)
  const catalog = useQuery({
    queryKey: ['model-filter-catalog', user?.id, user?.group, user?.role, sid],
    enabled: Boolean(user?.id) && !props.disabled,
    queryFn: async () => {
      const response = requireServerSuccess(await getUserModels())
      if (
        !Array.isArray(response.data) ||
        response.data.some((model) => typeof model !== 'string')
      ) {
        throw new Error('Invalid model catalog')
      }
      return response.data
    },
    staleTime: 60_000,
    retry: false,
    meta: { errorToast: false },
  })
  const models = [
    ...new Set(
      [...(catalog.data ?? []), ...(props.models ?? []), props.value].filter(
        (model) => model.trim().length > 0
      )
    ),
  ].sort()

  return (
    <div className='min-w-0 space-y-1'>
      <Combobox
        id={props.id}
        aria-label={props['aria-label'] ?? t('Model Name')}
        aria-invalid={props['aria-invalid']}
        disabled={props.disabled}
        value={props.value}
        onValueChange={(value) => props.onValueChange(value ?? '')}
        onKeyDown={(event) => {
          // Enter in an unselected search must not submit the surrounding form.
          if (
            event.key === 'Enter' &&
            !event.currentTarget.getAttribute('aria-activedescendant')
          ) {
            event.preventDefault()
          }
        }}
        options={[
          { value: '', label: t('All models') },
          ...models.map((model) => ({ value: model, label: model })),
        ]}
        placeholder={t('All models')}
        emptyText={t('No matching models')}
        className={props.className}
        popupClassName={props.popupClassName}
      />
      {!props.disabled && catalog.isFetching && (
        <div role='status'>
          <LoadingState inline size='sm' message={t('Loading...')} />
        </div>
      )}
      {!props.disabled && catalog.isError && !catalog.isFetching && (
        <div
          role='status'
          className='flex flex-wrap items-center gap-x-2 text-xs'
        >
          <span>{t('Failed to load models')}</span>
          <Button
            type='button'
            variant='link'
            size='sm'
            className='h-auto px-0 py-1 text-xs'
            onClick={() => void catalog.refetch()}
          >
            {t('Retry model catalog')}
          </Button>
        </div>
      )}
    </div>
  )
}
