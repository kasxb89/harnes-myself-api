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
import { useQueryClient } from '@tanstack/react-query'
import { useNavigate, useSearch } from '@tanstack/react-router'
import { lazy, Suspense, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { canAccessAdminRoute } from '@/lib/admin-permissions'
import { useAuthStore } from '@/stores/auth-store'

import { resolveMediaManagementTab, type MediaManagementTab } from './search'

const MediaOperationsReport = lazy(() =>
  import('@/features/media-operations').then((module) => ({
    default: module.MediaOperationsReport,
  }))
)
const VideoUsageReport = lazy(() =>
  import('@/features/video-usage').then((module) => ({
    default: module.VideoUsageReport,
  }))
)
const MediaConcurrencyPanel = lazy(() =>
  import('@/features/media-operations/components/concurrency-panel').then(
    (module) => ({ default: module.MediaConcurrencyPanel })
  )
)

export function MediaManagementPage(props: { kind?: 'image' | 'video' } = {}) {
  const { t } = useTranslation()
  const user = useAuthStore((state) => state.auth.user)
  const canReadMedia = canAccessAdminRoute(user, '/media-operations')
  const canReadVideo =
    props.kind !== 'image' && canAccessAdminRoute(user, '/video-usage')
  if (!user || (!canReadMedia && !canReadVideo)) {
    return <ErrorState title={t('Access denied')} />
  }
  return (
    <MediaManagementReports
      key={`${user.id}:${props.kind ?? 'all'}:${canReadMedia}:${canReadVideo}`}
      userId={user.id}
      kind={props.kind}
      canReadMedia={canReadMedia}
      canReadVideo={canReadVideo}
    />
  )
}

function MediaManagementReports(props: {
  userId: number
  kind?: 'image' | 'video'
  canReadMedia: boolean
  canReadVideo: boolean
}) {
  const { t } = useTranslation()
  const search = useSearch({ strict: false })
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const tab = resolveMediaManagementTab(
    search.tab === 'statistics' ? 'video' : search.tab,
    props.canReadMedia,
    props.canReadVideo
  )
  let path: '/image-management' | '/video-management' | '/media-management' =
    '/media-management'
  if (props.kind === 'image') path = '/image-management'
  if (props.kind === 'video') path = '/video-management'
  const searchTab = props.kind && tab === 'video' ? 'statistics' : tab
  const [visited, setVisited] = useState<MediaManagementTab[]>([tab])
  useEffect(
    () => () => {
      for (const feature of [
        'media-operations',
        'media-concurrency',
        'video-usage',
      ]) {
        const queryKey = [feature, props.userId]
        void queryClient.cancelQueries({ queryKey })
        queryClient.removeQueries({ queryKey })
      }
    },
    [props.userId, queryClient]
  )
  useEffect(() => {
    setVisited((previous) =>
      previous.includes(tab) ? previous : [...previous, tab]
    )
    if (search.tab !== searchTab) {
      void navigate({
        to: path,
        search: { tab: searchTab },
        replace: true,
        resetScroll: false,
      })
    }
  }, [tab, searchTab, search.tab, navigate, path])
  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {props.kind === 'image' && t('Image management')}
        {props.kind === 'video' && t('Video management')}
        {!props.kind && t('Image and video management')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <Tabs
          value={tab}
          onValueChange={(value) => {
            const next = resolveMediaManagementTab(
              value,
              props.canReadMedia,
              props.canReadVideo
            )
            void navigate({
              to: path,
              search: {
                tab: props.kind && next === 'video' ? 'statistics' : next,
              },
              resetScroll: false,
            })
          }}
          className='min-w-0 gap-4'
        >
          <TabsList
            aria-label={t('Image and video management sections')}
            className='max-w-full flex-wrap justify-start gap-1 group-data-horizontal/tabs:h-auto'
          >
            {props.canReadMedia && (
              <TabsTrigger
                value='tasks'
                className='min-h-8 px-3 whitespace-normal'
              >
                {t('Task monitoring')}
              </TabsTrigger>
            )}
            {props.canReadMedia && (
              <TabsTrigger
                value='concurrency'
                className='min-h-8 px-3 whitespace-normal'
              >
                {t('Concurrency settings')}
              </TabsTrigger>
            )}
            {props.canReadVideo && (
              <TabsTrigger
                value='video'
                className='min-h-8 px-3 whitespace-normal'
              >
                {t('Video statistics')}
              </TabsTrigger>
            )}
          </TabsList>
          <Suspense fallback={<LoadingState inline />}>
            {props.canReadMedia && (
              <TabsContent value='tasks' keepMounted>
                {(tab === 'tasks' || visited.includes('tasks')) && (
                  <MediaOperationsReport
                    userId={props.userId}
                    kind={props.kind}
                    embedded
                    active={tab === 'tasks'}
                  />
                )}
              </TabsContent>
            )}
            {props.canReadMedia && (
              <TabsContent value='concurrency' keepMounted>
                {(tab === 'concurrency' || visited.includes('concurrency')) && (
                  <MediaConcurrencyPanel
                    userId={props.userId}
                    kind={props.kind}
                    active={tab === 'concurrency'}
                  />
                )}
              </TabsContent>
            )}
            {props.canReadVideo && (
              <TabsContent value='video' keepMounted>
                {(tab === 'video' || visited.includes('video')) && (
                  <VideoUsageReport
                    userId={props.userId}
                    embedded
                    active={tab === 'video'}
                  />
                )}
              </TabsContent>
            )}
          </Suspense>
        </Tabs>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
