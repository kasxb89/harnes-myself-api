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
import { Pause, Play } from 'lucide-react'
import { useEffect, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { ObsidianObject } from '@/features/relay-portal/obsidian-object'
import { useMediaQuery } from '@/hooks/use-media-query'

/** One persistent visual stage for every console route, outside page transitions. */
export function ObsidianWorkspace(props: {
  children: ReactNode
  showStage?: boolean
}) {
  const { t } = useTranslation()
  const reducedMotion = useMediaQuery('(prefers-reduced-motion: reduce)')
  const [enabled, setEnabled] = useState(true)
  const [visible, setVisible] = useState(
    () => document.visibilityState !== 'hidden'
  )
  const running = enabled && !reducedMotion && visible

  useEffect(() => {
    const onVisibilityChange = () =>
      setVisible(document.visibilityState !== 'hidden')
    document.addEventListener('visibilitychange', onVisibilityChange)
    return () =>
      document.removeEventListener('visibilitychange', onVisibilityChange)
  }, [])

  return (
    <div
      className='obsidian-workspace flex min-h-0 flex-1 flex-col'
      data-effects-running={props.showStage === false ? undefined : running}
    >
      {props.showStage !== false && (
        <div className='obsidian-workspace-stage'>
          <div className='obsidian-workspace-tracks' aria-hidden='true'>
            <span className='obsidian-workspace-track' />
            <span className='obsidian-workspace-track obsidian-workspace-track-second' />
            <span className='obsidian-workspace-signal' />
          </div>
          <div className='obsidian-workspace-copy'>
            <p className='obsidian-workspace-eyebrow'>YUNSHU / WORKSPACE</p>
            <p className='obsidian-workspace-title'>{t('Your workspace')}</p>
            <p className='obsidian-workspace-description'>
              {t('Models, usage and requests, in one place.')}
            </p>
          </div>
          <div className='obsidian-workspace-art' aria-hidden='true'>
            <ObsidianObject running={running} />
          </div>
          <Button
            type='button'
            variant='outline'
            className='obsidian-workspace-toggle'
            aria-pressed={enabled && !reducedMotion}
            disabled={reducedMotion}
            title={
              reducedMotion
                ? t('System reduced motion is enabled')
                : t('Workspace effects')
            }
            onClick={() => setEnabled((current) => !current)}
          >
            {enabled && !reducedMotion ? (
              <Pause aria-hidden='true' />
            ) : (
              <Play aria-hidden='true' />
            )}
            <span className='obsidian-workspace-toggle-label'>
              {reducedMotion
                ? t('System reduced motion is enabled')
                : t(enabled ? 'Pause effects' : 'Enable effects')}
            </span>
          </Button>
        </div>
      )}
      {props.children}
    </div>
  )
}
