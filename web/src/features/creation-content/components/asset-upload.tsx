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
import { useMutation } from '@tanstack/react-query'
import { useId } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Field, FieldLabel, FieldDescription } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { handleServerError } from '@/lib/handle-server-error'

import { uploadAsset } from '../api'

type Props = {
  kind: 'image' | 'video'
  value: string | null
  onChange: (id: string | null) => void
  onBusyChange: (busy: boolean) => void
}
export function AssetUpload(props: Props) {
  const { t } = useTranslation()
  const id = useId()
  const upload = useMutation({
    mutationFn: (file: File) => {
      const limit =
        props.kind === 'image' ? 10 * 1024 * 1024 : 100 * 1024 * 1024
      const types =
        props.kind === 'image'
          ? ['image/png', 'image/jpeg', 'image/webp']
          : ['video/mp4', 'video/webm']
      if (
        file.size > limit ||
        (file.type !== '' && !types.includes(file.type))
      ) {
        throw new Error(
          t(
            'Invalid creation asset; use PNG, JPEG, WebP, MP4 or WebM within the size limit'
          )
        )
      }
      return uploadAsset(file)
    },
    onMutate: () => props.onBusyChange(true),
    onSuccess: (asset) => props.onChange(asset.id),
    onError: (error: unknown) => handleServerError(error),
    onSettled: () => props.onBusyChange(false),
  })
  const title =
    props.kind === 'image' ? t('Template cover') : t('Demo video (optional)')
  return (
    <Field>
      <FieldLabel htmlFor={id}>{title}</FieldLabel>
      <Input
        id={id}
        type='file'
        disabled={upload.isPending}
        accept={
          props.kind === 'image'
            ? 'image/png,image/jpeg,image/webp'
            : 'video/mp4,video/webm'
        }
        onChange={(event) => {
          const file = event.target.files?.[0]
          event.target.value = ''
          if (file) upload.mutate(file)
        }}
      />
      <FieldDescription>
        {props.kind === 'image'
          ? t('PNG, JPEG or WebP, up to 10 MiB')
          : t('MP4 or WebM, up to 100 MiB')}
      </FieldDescription>
      {upload.isPending && <p role='status'>{t('Uploading...')}</p>}
      {props.value && (
        <div className='flex flex-col items-start gap-2'>
          {props.kind === 'image' ? (
            <img
              src={`/api/creation/assets/${props.value}`}
              alt={title}
              className='max-h-40 max-w-full rounded-md object-contain'
            />
          ) : (
            <video
              src={`/api/creation/assets/${props.value}`}
              controls
              preload='metadata'
              aria-label={title}
              className='max-h-48 max-w-full rounded-md'
            />
          )}
          <Button
            type='button'
            variant='outline'
            size='sm'
            disabled={upload.isPending}
            onClick={() => props.onChange(null)}
          >
            {t('Remove asset')}
          </Button>
        </div>
      )}
    </Field>
  )
}
