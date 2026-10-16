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
import { useId } from 'react'
import { useTranslation } from 'react-i18next'

import {
  Field,
  FieldGroup,
  FieldLabel,
  FieldDescription,
} from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'

import type { LocalizedText } from '../lib/schema'

type Props = {
  label: string
  value: LocalizedText
  onChange: (value: LocalizedText) => void
  maxLength: number
  multiline?: boolean
  required?: boolean
  invalid?: boolean
}
export function LocalizedField(props: Props) {
  const { t } = useTranslation()
  const id = useId()
  const Control = props.multiline ? Textarea : Input
  return (
    <FieldGroup className='grid gap-3 sm:grid-cols-2'>
      <Field data-invalid={props.invalid}>
        <FieldLabel htmlFor={`${id}-zh`}>
          {props.label} · {t('Chinese')}
        </FieldLabel>
        <Control
          id={`${id}-zh`}
          value={props.value.zh}
          required={props.required}
          maxLength={props.maxLength}
          aria-invalid={props.invalid}
          onChange={(event) =>
            props.onChange({ ...props.value, zh: event.target.value })
          }
        />
        {props.invalid && (
          <FieldDescription>
            {t('Check the required text and length limits')}
          </FieldDescription>
        )}
      </Field>
      <Field>
        <FieldLabel htmlFor={`${id}-en`}>
          {props.label} · {t('English (optional)')}
        </FieldLabel>
        <Control
          id={`${id}-en`}
          value={props.value.en ?? ''}
          maxLength={props.maxLength}
          onChange={(event) =>
            props.onChange({ ...props.value, en: event.target.value })
          }
        />
      </Field>
    </FieldGroup>
  )
}
