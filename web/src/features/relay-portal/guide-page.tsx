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
import { Link } from '@tanstack/react-router'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { useSystemConfig } from '@/hooks/use-system-config'
import { statusQueryOptions } from '@/lib/status-query'

import { RelayAccessGuide } from './guide'
import { normalizeRelayRoot } from './guide-content'

import './obsidian.css'

export function RelayGuidePage() {
  const { t } = useTranslation()
  const { systemName, logo } = useSystemConfig()
  const status = useQuery(statusQueryOptions)
  return (
    <div className='relay-obsidian-guide relay-obsidian-guide-page bg-background text-foreground min-h-svh'>
      <header className='mx-auto flex max-w-6xl items-center justify-between gap-4 border-b px-5 py-5 sm:px-8'>
        <Link to='/' className='flex min-w-0 items-center gap-3 font-semibold'>
          <img src={logo} alt='' className='h-8 w-8 object-contain' />
          <span>{systemName}</span>
        </Link>
        <Button render={<Link to='/sign-up' search={{ design: 'obsidian' }} />}>
          {t('Create account')}
        </Button>
      </header>
      <main className='mx-auto max-w-6xl px-5 py-8 sm:px-8 sm:py-12'>
        <RelayAccessGuide
          rootURL={normalizeRelayRoot(status.data?.server_address)}
        />
      </main>
    </div>
  )
}
