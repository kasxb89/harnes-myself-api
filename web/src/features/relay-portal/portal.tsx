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
import { ArrowDownRight, ArrowRight, BookOpen, Pause, Play } from 'lucide-react'
import { lazy, Suspense, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

import './portal.css'
import './obsidian.css'

const ObsidianObject = lazy(() =>
  import('./obsidian-object').then((module) => ({
    default: module.ObsidianObject,
  }))
)

export type RelayPortalProps = {
  onReadGuide: () => void
  children: ReactNode
  systemName: string
  logo: string
}

export function RelayPortal({
  onReadGuide,
  children,
  systemName,
  logo,
}: RelayPortalProps) {
  const { t } = useTranslation()
  const [effectsEnabled, setEffectsEnabled] = useState(true)
  return (
    <div
      className='relay-portal'
      data-theme='obsidian'
      data-effects={effectsEnabled ? 'running' : 'paused'}
    >
      <header className='relay-portal-header'>
        <Link to='/' className='relay-portal-brand'>
          <img src={logo} alt='' width={36} height={36} />
          <span>{systemName}</span>
        </Link>
        <nav
          className='relay-portal-actions'
          aria-label={t('Relay account navigation')}
        >
          <Button
            type='button'
            variant='ghost'
            className='relay-portal-action relay-effects-control'
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
          <Button
            type='button'
            variant='ghost'
            className='relay-portal-action relay-portal-guide-action'
            onClick={onReadGuide}
          >
            <BookOpen aria-hidden='true' />
            <span>{t('Connection handbook')}</span>
          </Button>
          <Link to='/sign-in' className='relay-portal-login'>
            {t('Sign in')}
            <ArrowRight aria-hidden='true' />
          </Link>
        </nav>
      </header>

      <main className='relay-portal-stage'>
        <section
          className='relay-portal-hero'
          aria-labelledby='relay-portal-title'
        >
          <p className='relay-portal-eyebrow'>
            {t('AI model relay for developers and creators')}
          </p>
          <h1 id='relay-portal-title'>{t('One API. Your entire workflow.')}</h1>
          <p className='relay-portal-description'>
            {t(
              'Create your account, get an API key, and configure the tools you already use.'
            )}
          </p>
          <Button
            type='button'
            variant='ghost'
            className='relay-hero-guide'
            onClick={onReadGuide}
          >
            {t('Read the connection handbook')}
            <ArrowDownRight aria-hidden='true' />
          </Button>
          <div className='relay-obsidian-visual' aria-hidden='true'>
            <Suspense
              fallback={<div className='relay-obsidian-object-fallback' />}
            >
              <ObsidianObject running={effectsEnabled} />
            </Suspense>
          </div>
        </section>

        <section
          className='relay-registration'
          aria-labelledby='relay-registration-title'
        >
          <div className='relay-registration-heading'>
            <p>{t('Your next step')}</p>
            <h2 id='relay-registration-title'>{t('Create an account')}</h2>
          </div>
          <div className='relay-registration-content'>{children}</div>
        </section>

        <figure
          className='relay-protocol'
          aria-label={t(
            'Tools connect through a relay API to your selected model'
          )}
        >
          <div className='relay-protocol-title'>
            <span>{t('A connection you control')}</span>
          </div>
          <div className='relay-request-example'>
            <p>
              <span>{t('Request example')}</span>
            </p>
            <pre>
              <code>
                <span className='relay-code-method'>POST</span>
                {` /v1/chat/completions\n`}
                <span className='relay-code-key'>Authorization</span>
                {`: Bearer YOUR_API_KEY\n\n{\n  "model": "YOUR_MODEL_ID",\n  "messages": [{ "role": "user",\n    "content": "Hello" }]\n}`}
              </code>
            </pre>
          </div>
          <p className='relay-protocol-note'>
            {t(
              'Example parameters. Use your own API key and an available model ID.'
            )}
          </p>
        </figure>
      </main>
      <footer className='relay-portal-footer'>
        <span>{systemName}</span>
        <span>{t('Your account. Your tools. Your next idea.')}</span>
      </footer>
    </div>
  )
}
