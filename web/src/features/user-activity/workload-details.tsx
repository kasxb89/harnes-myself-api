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

import { StaticDataTable } from '@/components/data-table'
import { EmptyState } from '@/components/empty-state'
import { Alert, AlertDescription } from '@/components/ui/alert'
import {
  DetailRow,
  DetailSection,
} from '@/features/usage-logs/components/dialogs/log-detail-layout'

import { ActivityTimestamp, ObservationStatus } from './display'
import type {
  ActivityCollection,
  ClientWorkloadDetail,
  WorkloadActivity,
} from './types'

export function WorkloadDetails(props: { workload?: ClientWorkloadDetail }) {
  const { t } = useTranslation()
  const data = props.workload?.schema_version === 1 ? props.workload : undefined
  const kinds: Record<ActivityCollection, string> = {
    chat_activity: t('Chat activity'),
    project_run: t('Project activity'),
    storyboard_task: t('Media task attempt'),
    local_operation: t('Local operation'),
    shots: t('Shot plan collection'),
  }
  const phases: Record<string, string> = {
    busy: t('Chat busy'),
    waiting: t('Waiting'),
    preparing: t('Preparing'),
    submitting: t('Submitting'),
    accepted: t('Accepted'),
    queued: t('Queued'),
    generating: t('Provider generating'),
    saving: t('Saving'),
    editing: t('Editing'),
    downloading: t('Downloading'),
    composing: t('Composing'),
    publishing: t('Publishing'),
    unknown: t('State unknown'),
    ended: t('Explicitly ended'),
  }
  const states: Record<WorkloadActivity['work_state'], string> = {
    active: t('Active work'),
    unknown: t('State unknown'),
    ended: t('Explicitly ended'),
  }
  const outcomes: Record<NonNullable<WorkloadActivity['outcome']>, string> = {
    succeeded: t('Reported success'),
    failed: t('Reported failure'),
    cancelled: t('Reported cancellation'),
    interrupted: t('Reported interruption'),
  }
  const identifiers = (row: WorkloadActivity) =>
    [
      [t('Activity identifier'), row.activity_id],
      [t('Root chat identifier'), row.chat_session_id],
      [t('Project identifier'), row.project_id],
      [t('Parent activity identifier'), row.parent_activity_id],
      [t('Attempt identifier'), row.attempt_id],
      [t('API task identifier'), row.api_task_id],
      [t('Local job identifier'), row.local_job_id],
      [t('Shot identifiers'), row.shot_ids?.join(', ')],
    ].filter((pair) => Boolean(pair[1]))
  return (
    <DetailSection label={t('Observed workload details')}>
      <p className='text-muted-foreground mb-3 text-xs'>
        {t(
          'Reported outcomes describe client observations, not provider settlement, refunds or proof of task cancellation.'
        )}
      </p>
      {!data || data.observation_status === 'not_collected' ? (
        <EmptyState
          title={t('Not collected')}
          description={t(
            'This client has no collected workload snapshot. Presence and account usage remain separate.'
          )}
          className='min-h-28'
        />
      ) : (
        <div className='space-y-4'>
          <DetailRow
            label={t('Observation status')}
            value={<ObservationStatus status={data.observation_status} />}
          />
          <DetailRow
            label={t('Last accepted observation')}
            value={<ActivityTimestamp value={data.accepted_at} />}
          />
          <StaticDataTable
            data={Object.entries(kinds) as [ActivityCollection, string][]}
            getRowKey={(row) => row[0]}
            columns={[
              { id: 'kind', header: t('Collection'), cell: (row) => row[1] },
              {
                id: 'collected',
                header: t('Collection status'),
                cell: (row) =>
                  data.collections[row[0]] === 'collected'
                    ? t('Collected')
                    : t('Not collected'),
              },
            ]}
          />
          {data.truncated && (
            <Alert>
              <AlertDescription>
                {t(
                  'Only the most recent 256 workload and shot observations are shown. This is not a complete history.'
                )}
              </AlertDescription>
            </Alert>
          )}
          <h3 className='text-sm font-medium'>{t('Activity observations')}</h3>
          {data.activities.length ? (
            <StaticDataTable
              data={data.activities}
              getRowKey={(row) => row.activity_id}
              columns={[
                {
                  id: 'kind',
                  header: t('Activity kind'),
                  cell: (row) => kinds[row.kind] ?? t('State unknown'),
                },
                {
                  id: 'state',
                  header: t('Reported state and phase'),
                  cell: (row) => (
                    <div className='space-y-1'>
                      <div>{states[row.work_state] ?? t('State unknown')}</div>
                      <div>{phases[row.phase] ?? t('State unknown')}</div>
                      {row.outcome && (
                        <div>{outcomes[row.outcome] ?? t('State unknown')}</div>
                      )}
                    </div>
                  ),
                },
                {
                  id: 'observation',
                  header: t('Observation status'),
                  cell: (row) => (
                    <ObservationStatus status={row.observation_status} />
                  ),
                },
                {
                  id: 'ids',
                  header: t('Safe workload identifiers'),
                  cell: (row) => (
                    <dl className='max-w-80 space-y-2 text-xs'>
                      {identifiers(row).map(([label, value]) => (
                        <div key={label}>
                          <dt className='text-muted-foreground'>{label}</dt>
                          <dd className='break-all'>{value}</dd>
                        </div>
                      ))}
                    </dl>
                  ),
                },
              ]}
            />
          ) : (
            <EmptyState
              title={t('No retained activity observations')}
              className='min-h-24'
            />
          )}
          <h3 className='text-sm font-medium'>
            {t('Shot planning observations')}
          </h3>
          {data.shots.length ? (
            <StaticDataTable
              data={data.shots}
              getRowKey={(row) => `${row.project_id}:${row.shot_id}`}
              columns={[
                {
                  id: 'project',
                  header: t('Project identifier'),
                  cell: (row) => (
                    <span className='break-all'>{row.project_id}</span>
                  ),
                },
                {
                  id: 'shot',
                  header: t('Shot identifier'),
                  cell: (row) => (
                    <span className='break-all'>{row.shot_id}</span>
                  ),
                },
                {
                  id: 'status',
                  header: t('Observation status'),
                  cell: (row) => (
                    <ObservationStatus status={row.observation_status} />
                  ),
                },
              ]}
            />
          ) : (
            <EmptyState
              title={t('No retained shot observations')}
              className='min-h-24'
            />
          )}
        </div>
      )}
    </DetailSection>
  )
}
