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
import { LayoutDashboard } from 'lucide-react'
import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ErrorState } from '@/components/error-state'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { IconBadge } from '@/components/ui/icon-badge'
import { Switch } from '@/components/ui/switch'
import { canAccessAdminRoute } from '@/lib/admin-permissions'
import { api } from '@/lib/api'
import { handleServerError } from '@/lib/handle-server-error'
import {
  parseSidebarModules,
  SIDEBAR_MODULE_SECTIONS,
  SIDEBAR_URL_MODULES,
  type SidebarModulesConfig,
} from '@/lib/sidebar-modules'
import { useAuthStore } from '@/stores/auth-store'

export function SidebarModulesCard() {
  const { t } = useTranslation()
  const [loading, setLoading] = useState(false)
  const [isLoading, setIsLoading] = useState(true)
  const [loadFailed, setLoadFailed] = useState(false)
  const [config, setConfig] = useState<SidebarModulesConfig>({})
  const currentUser = useAuthStore((s) => s.auth.user)
  const setUser = useAuthStore((s) => s.auth.setUser)

  const sectionDefs = SIDEBAR_MODULE_SECTIONS.map((section) => ({
    ...section,
    modules: section.modules.filter(
      (module) =>
        (section.key === 'chat' && module.key === 'chat') ||
        Object.entries(SIDEBAR_URL_MODULES).some(
          ([url, mapping]) =>
            mapping.section === section.key &&
            mapping.module === module.key &&
            canAccessAdminRoute(currentUser, url)
        )
    ),
  })).filter((section) => section.modules.length > 0)

  const loadConfig = useCallback(async () => {
    setIsLoading(true)
    setLoadFailed(false)
    try {
      const res = await api.get('/api/user/self')
      if (!res.data.success) {
        setLoadFailed(true)
        handleServerError(res.data, t('Failed to load sidebar settings'))
        return
      }
      const raw = res.data.data?.sidebar_modules
      setConfig(
        parseSidebarModules(
          typeof raw === 'string' ? raw : JSON.stringify(raw ?? {})
        )
      )
    } catch (error) {
      setLoadFailed(true)
      handleServerError(error, t('Failed to load sidebar settings'))
    } finally {
      setIsLoading(false)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  useEffect(() => {
    loadConfig()
  }, [loadConfig])

  const toggleSection = (sectionKey: string, val: boolean) => {
    setConfig((prev) => ({
      ...prev,
      [sectionKey]: { ...prev[sectionKey], enabled: val },
    }))
  }

  const toggleModule = (
    sectionKey: string,
    moduleKey: string,
    val: boolean
  ) => {
    setConfig((prev) => ({
      ...prev,
      [sectionKey]: { ...prev[sectionKey], [moduleKey]: val },
    }))
  }

  const handleSave = async () => {
    if (isLoading || loadFailed || loading) return
    setLoading(true)
    try {
      const serialized = JSON.stringify(config)
      const res = await api.put('/api/user/self', {
        sidebar_modules: serialized,
      })
      if (res.data.success) {
        // Sync to auth-store so useSidebarConfig re-runs and the sidebar
        // updates immediately without needing a page refresh.
        const latestUser = useAuthStore.getState().auth.user
        if (latestUser && latestUser.id === currentUser?.id) {
          setUser({ ...latestUser, sidebar_modules: serialized })
        }
        toast.success(t('Saved successfully'))
      } else {
        handleServerError(res.data, t('Save failed'))
      }
    } catch (error) {
      handleServerError(error, t('Save failed, please retry'))
    } finally {
      setLoading(false)
    }
  }

  const handleReset = () => {
    setConfig(parseSidebarModules(''))
    toast.success(t('Reset to default configuration'))
  }

  let content: ReactNode
  if (isLoading) {
    content = <LoadingState />
  } else if (loadFailed) {
    content = (
      <ErrorState
        title={t('Failed to load sidebar settings')}
        onRetry={() => void loadConfig()}
      />
    )
  } else {
    content = sectionDefs.map((section) => {
      const sectionEnabled = config[section.key]?.enabled !== false
      return (
        <div
          key={section.key}
          className='bg-background/60 rounded-xl border p-3'
        >
          <div className='flex items-start justify-between gap-3'>
            <div className='min-w-0'>
              <p className='text-sm font-medium'>{t(section.titleKey)}</p>
              <p className='text-muted-foreground text-xs'>
                {t(section.descriptionKey)}
              </p>
            </div>
            <Switch
              aria-label={t(section.titleKey)}
              checked={sectionEnabled}
              disabled={loading}
              onCheckedChange={(v) => toggleSection(section.key, v)}
            />
          </div>
          <div className='mt-3 grid grid-cols-1 gap-2 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-1'>
            {section.modules.map((mod) => (
              <div
                key={mod.key}
                className={`flex min-h-16 items-center justify-between rounded-lg border p-3 ${
                  sectionEnabled ? '' : 'opacity-50'
                }`}
              >
                <div className='mr-2 min-w-0'>
                  <p className='truncate text-sm font-medium'>
                    {t(mod.titleKey)}
                  </p>
                  <p className='text-muted-foreground truncate text-xs'>
                    {t(mod.descriptionKey)}
                  </p>
                </div>
                <Switch
                  aria-label={t(mod.titleKey)}
                  checked={config[section.key]?.[mod.key] !== false}
                  onCheckedChange={(v) => toggleModule(section.key, mod.key, v)}
                  disabled={!sectionEnabled || loading}
                />
              </div>
            ))}
          </div>
        </div>
      )
    })
  }

  return (
    <Card data-card-hover='false' className='gap-0 overflow-hidden py-0'>
      <CardHeader className='border-b p-3 !pb-3 sm:p-5 sm:!pb-5'>
        <div className='flex items-center gap-3'>
          <IconBadge tone='info' size='title'>
            <LayoutDashboard />
          </IconBadge>
          <div className='min-w-0'>
            <CardTitle className='text-lg tracking-tight sm:text-xl'>
              {t('Sidebar Personal Settings')}
            </CardTitle>
            <CardDescription className='text-xs sm:text-sm'>
              {t('Customize sidebar display content')}
            </CardDescription>
          </div>
        </div>
      </CardHeader>
      <CardContent className='space-y-4 p-3 sm:space-y-5 sm:p-5'>
        {content}

        <div className='flex flex-col-reverse gap-2 border-t pt-4 sm:flex-row sm:justify-end'>
          <Button
            variant='outline'
            onClick={handleReset}
            disabled={loading || isLoading || loadFailed}
          >
            {t('Reset to Default')}
          </Button>
          <Button
            onClick={handleSave}
            disabled={loading || isLoading || loadFailed}
          >
            {loading ? t('Saving...') : t('Save Changes')}
          </Button>
        </div>
      </CardContent>
    </Card>
  )
}
