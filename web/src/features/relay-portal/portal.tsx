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
import {
  ArrowDownRight,
  ArrowRight,
  BookOpen,
  Code2,
  Layers2,
  Pause,
  Play,
  Route,
} from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'

import { relayThemes, type RelayTheme } from './themes'

import './portal.css'

export type { RelayTheme } from './themes'

export type RelayPortalProps = {
  theme: RelayTheme
  comparison?: boolean
  onThemeChange?: (theme: RelayTheme) => void
  onReadGuide: () => void
  children: ReactNode
  systemName: string
  logo: string
}

export function RelayPortal({
  theme,
  comparison = false,
  onThemeChange,
  onReadGuide,
  children,
  systemName,
  logo,
}: RelayPortalProps) {
  const { t } = useTranslation()
  const [effectsEnabled, setEffectsEnabled] = useState(true)
  const current =
    relayThemes.find((item) => item.id === theme) ?? relayThemes[0]
  return (
    <div
      className='relay-portal'
      data-theme={theme}
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

      {comparison && (
        <nav
          className='relay-theme-nav'
          aria-label={t('Compare registration designs')}
        >
          <span className='relay-theme-caption'>{t('Choose your view')}</span>
          <div className='relay-theme-options'>
            {relayThemes.map((item) => (
              <Button
                key={item.id}
                type='button'
                variant='ghost'
                aria-pressed={theme === item.id}
                className='relay-theme-option'
                disabled={!onThemeChange}
                onClick={() => onThemeChange?.(item.id)}
              >
                <span
                  className='relay-theme-swatch'
                  data-swatch={item.id}
                  aria-hidden='true'
                />
                <span>{t(item.nameKey)}</span>
              </Button>
            ))}
          </div>
        </nav>
      )}

      <main className='relay-portal-stage'>
        <section
          className='relay-portal-hero'
          aria-labelledby='relay-portal-title'
        >
          <RelayEffectStage zone='hero' />
          <p className='relay-portal-eyebrow'>
            <span aria-hidden='true' />
            {t('AI model relay for developers and creators')}
          </p>
          <h1 id='relay-portal-title'>{t(current.heroKey)}</h1>
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
          <div
            className='relay-portal-chapters'
            aria-label={t('Inside the handbook')}
          >
            <span className='relay-chapters-label'>
              {t('Inside the handbook')}
            </span>
            <ol>
              <li>{t('Account and API key')}</li>
              <li>{t('Endpoint and protocol')}</li>
              <li>{t('Tool configuration')}</li>
              <li>{t('Model selection')}</li>
            </ol>
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
          <RelayEffectStage zone='protocol' />
          <div className='relay-protocol-title'>
            <span>{t('A connection you control')}</span>
            <Route aria-hidden='true' />
          </div>
          <div className='relay-route-visual'>
            <span className='relay-signal-flow' aria-hidden='true' />
            <svg
              className='relay-route-lines'
              viewBox='0 0 600 180'
              fill='none'
              aria-hidden='true'
              preserveAspectRatio='none'
            >
              <path d='M100 92H500' />
              <path d='M100 92C190 92 175 24 300 24C425 24 410 92 500 92' />
              <circle cx='300' cy='92' r='58' />
              <circle cx='100' cy='92' r='6' />
              <circle cx='500' cy='92' r='6' />
            </svg>
            <div className='relay-route-node' data-node='tools'>
              <Code2 aria-hidden='true' />
              <strong>{t('Your tools')}</strong>
              <span>{t('Client configuration')}</span>
            </div>
            <div className='relay-route-node' data-node='relay'>
              <Route aria-hidden='true' />
              <strong>{t('Relay API')}</strong>
              <span>/v1</span>
            </div>
            <div className='relay-route-node' data-node='model'>
              <Layers2 aria-hidden='true' />
              <strong>{t('Your model')}</strong>
              <span>model_id</span>
            </div>
          </div>
          <div className='relay-request-example'>
            <p>
              <span>{t('Request example')}</span>
              <span>JSON</span>
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

function RelayEffectStage({ zone }: { zone: 'hero' | 'protocol' }) {
  return (
    <div
      className='relay-effect-stage'
      data-effect-zone={zone}
      aria-hidden='true'
    >
      <span className='relay-effect-grid' />
      <span className='relay-effect-ring relay-effect-ring-a' />
      <span className='relay-effect-ring relay-effect-ring-b' />
      <span className='relay-effect-band relay-effect-band-a' />
      <span className='relay-effect-band relay-effect-band-b' />
      <span className='relay-effect-beam relay-effect-beam-a' />
      <span className='relay-effect-beam relay-effect-beam-b' />
      <span className='relay-effect-orbit'>
        <span />
      </span>
    </div>
  )
}
