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
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { CopyButton } from '@/components/copy-button'
import { Button, buttonVariants } from '@/components/ui/button'
import { Markdown } from '@/components/ui/markdown'

import {
  buildRelayAccessGuide,
  getRelayAccessGuideSections,
  RELAY_ACCESS_GUIDE_FILENAME,
  RELAY_ACCESS_GUIDE_INTRO,
} from './guide-content'

interface RelayAccessGuideProps {
  rootURL?: string
}

export function RelayAccessGuide(props: RelayAccessGuideProps) {
  const { t } = useTranslation()
  const sections = useMemo(
    () => getRelayAccessGuideSections(props.rootURL),
    [props.rootURL]
  )
  const guide = useMemo(
    () => buildRelayAccessGuide(props.rootURL),
    [props.rootURL]
  )

  function downloadGuide() {
    const blob = new Blob([guide], { type: 'text/markdown;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = RELAY_ACCESS_GUIDE_FILENAME
    anchor.click()
    URL.revokeObjectURL(url)
  }

  return (
    <div className='flex min-w-0 flex-col gap-6'>
      <header className='flex flex-col items-start justify-between gap-4 sm:flex-row sm:flex-wrap'>
        <div className='flex w-full min-w-0 flex-col gap-2 sm:w-auto sm:flex-1'>
          <h2 className='text-2xl font-semibold tracking-tight'>
            {t('Relay access guide')}
          </h2>
          <p
            lang='zh-CN'
            className='text-muted-foreground max-w-2xl text-base leading-relaxed'
          >
            {RELAY_ACCESS_GUIDE_INTRO}
          </p>
        </div>
        <div className='flex flex-wrap gap-2 [&_button]:min-h-11'>
          <CopyButton
            value={guide}
            variant='outline'
            size='default'
            aria-label={t('Copy guide')}
          >
            {t('Copy guide')}
          </CopyButton>
          <Button type='button' variant='secondary' onClick={downloadGuide}>
            {t('Download Markdown')}
          </Button>
        </div>
      </header>
      <nav
        aria-label={t('Guide contents')}
        className='flex flex-wrap gap-2 [&_a]:min-h-11'
      >
        {sections.map((section) => (
          <a
            key={section.id}
            href={`#relay-guide-${section.id}`}
            className={buttonVariants({ variant: 'outline' })}
          >
            {t(section.titleKey)}
          </a>
        ))}
      </nav>
      <article
        lang='zh-CN'
        aria-label={t('Relay access guide')}
        className='flex min-w-0 flex-col gap-8'
      >
        {sections.map((section) => (
          <section
            key={section.id}
            id={`relay-guide-${section.id}`}
            className='min-w-0 scroll-mt-6'
          >
            <Markdown className='text-base'>{section.markdown}</Markdown>
          </section>
        ))}
      </article>
    </div>
  )
}
