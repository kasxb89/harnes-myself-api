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
import { Link } from '@tanstack/react-router'
import { ArrowDownRight, BookOpen, Pause, Play } from 'lucide-react'
import { lazy, Suspense, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Skeleton } from '@/components/ui/skeleton'
import { useSystemConfig } from '@/hooks/use-system-config'

import './auth-world.css'

const ObsidianObject = lazy(() =>
  import('../relay-portal/obsidian-object').then((module) => ({
    default: module.ObsidianObject,
  }))
)

type AuthLayoutProps = {
  children: React.ReactNode
  variant?: 'compact' | 'sign-in'
}

export function AuthLayout({ children, variant = 'compact' }: AuthLayoutProps) {
  const { t } = useTranslation()
  const { systemName, logo, loading } = useSystemConfig()
  const [effectsEnabled, setEffectsEnabled] = useState(true)

  return (
    <div
      className='auth-world'
      data-variant={variant}
      data-effects={effectsEnabled ? 'running' : 'paused'}
    >
      <header className='auth-world-header'>
        <Link to='/' className='auth-world-brand'>
          <div className='relative h-8 w-8 shrink-0'>
            {loading ? (
              <Skeleton className='absolute inset-0 rounded-full' />
            ) : (
              <img
                src={logo}
                alt={t('Logo')}
                className='h-8 w-8 object-contain'
              />
            )}
          </div>
          {loading ? (
            <Skeleton className='h-6 w-24' />
          ) : (
            <span>{systemName}</span>
          )}
        </Link>
        <nav
          className='auth-world-actions'
          aria-label={t('Relay account navigation')}
        >
          {variant === 'sign-in' && (
            <Button
              type='button'
              variant='ghost'
              className='auth-world-action'
              aria-pressed={effectsEnabled}
              onClick={() => setEffectsEnabled((enabled) => !enabled)}
            >
              {effectsEnabled ? (
                <Pause aria-hidden='true' />
              ) : (
                <Play aria-hidden='true' />
              )}
              <span>
                {t(effectsEnabled ? 'Pause effects' : 'Enable effects')}
              </span>
            </Button>
          )}
          <Link to='/relay-guide' className='auth-world-action'>
            <BookOpen aria-hidden='true' />
            <span>{t('Connection handbook')}</span>
          </Link>
        </nav>
      </header>
      <main className='auth-world-main'>
        {variant === 'sign-in' && (
          <section
            className='auth-world-hero'
            aria-labelledby='auth-world-title'
          >
            <p className='auth-world-eyebrow'>
              {t('AI model relay for developers and creators')}
            </p>
            <h1 id='auth-world-title'>{t('One API. Your entire workflow.')}</h1>
            <p className='auth-world-description'>
              {t('Your account. Your tools. Your next idea.')}
            </p>
            <Link to='/relay-guide' className='auth-world-hero-guide'>
              {t('Read the connection handbook')}
              <ArrowDownRight aria-hidden='true' />
            </Link>
            <div className='auth-world-object' aria-hidden='true'>
              <Suspense fallback={null}>
                <ObsidianObject running={effectsEnabled} />
              </Suspense>
            </div>
          </section>
        )}
        <section className='auth-world-form'>{children}</section>
      </main>
    </div>
  )
}
