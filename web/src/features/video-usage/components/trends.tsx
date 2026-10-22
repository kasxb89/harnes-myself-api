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
import { useTranslation } from 'react-i18next'

import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'

import { useVideoFormat } from '../lib/format'
import type { VideoUsageResult } from '../schema'
import { VideoTrendChart } from './trend-chart'
import { VideoTrendValues } from './trend-values'

export default function VideoTrends(props: {
  trends: VideoUsageResult['trends']
  ingress: VideoUsageResult['ingress']
  onRecentRange: (seconds: 300 | 3600) => void
}) {
  const { t } = useTranslation()
  const format = useVideoFormat()
  const hasIngress = props.ingress.points.some(
    (point) => point.coverage === 'complete'
  )
  return (
    <section aria-label={t('Video trends')} className='min-w-0 space-y-4'>
      <div className='flex flex-wrap gap-2'>
        <Button variant='outline' onClick={() => props.onRecentRange(300)}>
          {t('Last 5 minutes')}
        </Button>
        <Button variant='outline' onClick={() => props.onRecentRange(3600)}>
          {t('Last 1 hour')}
        </Button>
      </div>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Task charts use all applied filters. Half-open buckets use actual clipped seconds; unknown samples remain gaps.'
        )}
      </p>
      <div className='grid min-w-0 gap-4 xl:grid-cols-2'>
        <Card className='min-w-0'>
          <CardHeader>
            <CardTitle>{t('Accepted task rate')}</CardTitle>
            <CardDescription>
              {t('Recorded accepted tasks per second, not HTTP ingress QPS.')}
            </CardDescription>
          </CardHeader>
          <CardContent>
            <VideoTrendChart
              kind='acceptance'
              points={props.trends.points}
              title={t('Accepted task rate')}
            />
          </CardContent>
        </Card>
        <Card className='min-w-0'>
          <CardHeader>
            <CardTitle>{t('Recorded in-flight peak')}</CardTitle>
            <CardDescription>
              {t(
                'Peak of eligible gateway-recorded intervals in each bucket; upstream capacity remains unknown.'
              )}
              {props.trends.points.some(
                (point) => point.peak_exclusions.excluded_intervals > 0
              ) && (
                <span className='mt-1 block'>
                  {t('Excluded intervals may hide additional in-flight tasks.')}
                </span>
              )}
            </CardDescription>
          </CardHeader>
          <CardContent>
            <VideoTrendChart
              kind='peak'
              points={props.trends.points}
              title={t('Recorded in-flight peak')}
            />
          </CardContent>
        </Card>
      </div>
      <Card className='min-w-0'>
        <CardHeader>
          <CardTitle>{t('Video HTTP ingress QPS')}</CardTitle>
          <CardDescription>
            {t(
              'Requests / s; includes requests that fail authentication or end without acceptance.'
            )}
          </CardDescription>
        </CardHeader>
        <CardContent className='space-y-4'>
          <Alert>
            <AlertTitle>
              {t('All video ingress · current instance / process only')}
            </AlertTitle>
            <AlertDescription>
              <p>
                {t(
                  'Only recognized video paths that reach this Go application are counted; requests rejected by Nginx, TLS or HTTP parsing are not observed.'
                )}
              </p>
              <p>
                {t(
                  'Only the time range applies. Model, user, client source, channel and provider filters do not apply to this chart.'
                )}
              </p>
              <p>
                {t(
                  'In-memory retention is 1 hour, with no historical persistence. Restarts reset coverage; partial and unobserved buckets are unknown.'
                )}
              </p>
              <p>
                {t('Process started')}:{' '}
                {format.time(props.ingress.process_started_at)}
              </p>
              <p>
                {t('Observed coverage')}: [
                {format.time(props.ingress.coverage_start)},{' '}
                {format.time(props.ingress.coverage_end)})
              </p>
            </AlertDescription>
          </Alert>
          {!hasIngress && (
            <p role='status' className='text-muted-foreground text-sm'>
              {t(
                'No complete ingress buckets. Choose a recent range within 1 hour to inspect QPS.'
              )}
            </p>
          )}
          <VideoTrendChart
            kind='ingress'
            points={props.ingress.points}
            title={t('Video HTTP ingress QPS')}
          />
        </CardContent>
      </Card>
      <VideoTrendValues
        points={props.trends.points}
        ingress={props.ingress.points}
      />
    </section>
  )
}
