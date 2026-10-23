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
import {
  Clapperboard,
  FolderOpen,
  MessagesSquare,
  Workflow,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { StaticDataTable } from '@/components/data-table'
import { StatCard } from '@/features/dashboard/components/ui/stat-card'
import { toIntlLocale } from '@/i18n/languages'
import { formatNumber } from '@/lib/format'

import type {
  ActivityCollection,
  ClientWorkloadSummary,
  WorkloadCoverage,
} from './types'

export function WorkloadSummary(props: {
  workload?: ClientWorkloadSummary
  loading: boolean
  error: boolean
}) {
  const { t, i18n } = useTranslation()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const data = props.workload?.schema_version === 1 ? props.workload : undefined
  const coverage = (kinds: ActivityCollection[]): WorkloadCoverage => {
    const values = kinds.map(
      (kind) => data?.coverage.collections[kind]?.coverage
    )
    if (values.every((value) => value === 'complete')) return 'complete'
    return values.some((value) => value === 'partial' || value === 'complete')
      ? 'partial'
      : 'not_collected'
  }
  const value = (
    count: number | undefined,
    kinds: ActivityCollection[],
    uncertain = false
  ) => {
    if (count === undefined) return t('Not collected')
    const state = coverage(kinds)
    if (state === 'complete' && !uncertain) return formatNumber(count, locale)
    if (state === 'not_collected' && count === 0) return t('Not collected')
    return t('Known count: {{count}}', { count: formatNumber(count, locale) })
  }
  const labels: Record<WorkloadCoverage, string> = {
    complete: t('Complete current coverage'),
    partial: t('Partial current coverage'),
    not_collected: t('Not collected'),
  }
  const collections: { key: ActivityCollection; label: string }[] = [
    { key: 'chat_activity', label: t('Chat activity collection') },
    { key: 'project_run', label: t('Project collection') },
    { key: 'storyboard_task', label: t('Media task collection') },
    { key: 'local_operation', label: t('Local operation collection') },
    { key: 'shots', label: t('Shot plan collection') },
  ]
  const cards = [
    {
      title: t('Generating chat sessions'),
      value: value(
        data?.known.generating_chat_sessions,
        ['chat_activity', 'project_run', 'storyboard_task', 'local_operation'],
        Boolean(data?.uncertainty.unknown_chat_sessions)
      ),
      description: t(
        'Distinct real root chats with observed work; task attempts are counted separately.'
      ),
      icon: MessagesSquare,
    },
    {
      title: t('Active projects'),
      value: value(
        data?.known.projects,
        ['project_run'],
        Boolean(data?.uncertainty.unknown_projects)
      ),
      description: t(
        'Distinct projects with a current known active observation'
      ),
      icon: FolderOpen,
    },
    {
      title: t('Media task attempts'),
      value: value(
        data?.known.task_attempts,
        ['storyboard_task'],
        Boolean(
          data?.uncertainty.unknown_task_attempts ||
          data?.uncertainty.conflicting_task_attempts
        )
      ),
      description: t(
        'Distinct active attempts; accepted, queued and generating are different phases.'
      ),
      icon: Clapperboard,
    },
    {
      title: t('Local operations'),
      value: value(data?.known.local_operations, ['local_operation']),
      description: t(
        'Observed downloading, saving, composing or publishing work'
      ),
      icon: Workflow,
    },
  ]
  const counts = data
    ? [
        {
          label: t('Planned shots'),
          value: value(data.known.planned_shots, ['shots']),
        },
        {
          label: t('Editing projects'),
          value: value(data.known.editing_projects, ['project_run']),
        },
        {
          label: t('Accepted tasks'),
          value: value(data.known.accepted_tasks, ['storyboard_task']),
        },
        {
          label: t('Queued tasks'),
          value: value(data.known.queued_tasks, ['storyboard_task']),
        },
        {
          label: t('Provider-generating tasks'),
          value: value(data.known.generating_tasks, ['storyboard_task']),
        },
        {
          label: t('Saving task results'),
          value: value(data.known.saving_tasks, ['storyboard_task']),
        },
      ]
    : []
  const uncertainty = data
    ? [
        {
          label: t('Unknown task attempts'),
          value: data.uncertainty.unknown_task_attempts,
        },
        {
          label: t('Conflicting task attempts'),
          value: data.uncertainty.conflicting_task_attempts,
        },
        {
          label: t('Unknown chat sessions'),
          value: data.uncertainty.unknown_chat_sessions,
        },
        {
          label: t('Unknown projects'),
          value: data.uncertainty.unknown_projects,
        },
        {
          label: t('Expired runtime observations'),
          value: data.uncertainty.expired_runtime_sessions,
        },
        {
          label: t('Unreported client processes'),
          value: data.uncertainty.unreported_runtime_sessions,
        },
      ]
    : []
  return (
    <section
      aria-label={t('Observed workload')}
      className='space-y-3 rounded-xl border p-3 sm:p-4'
    >
      <h2 className='text-sm font-medium'>{t('Observed workload')}</h2>
      <p className='text-muted-foreground text-xs'>
        {t(
          'Workload comes from explicit client observations, not model call logs. Unreported work is not measured zero.'
        )}
      </p>
      <div className='grid grid-cols-2 gap-3 sm:gap-5 xl:grid-cols-4'>
        {cards.map((card) => (
          <div role='group' aria-label={card.title} key={card.title}>
            <StatCard
              {...card}
              loading={props.loading}
              error={props.error}
              compactMobile
            />
          </div>
        ))}
      </div>
      {data && !props.error && (
        <>
          <div className='grid min-w-0 gap-4 xl:grid-cols-2'>
            <div className='min-w-0 space-y-2'>
              <h3 className='text-sm font-medium'>
                {t('Observed project and task phases')}
              </h3>
              <StaticDataTable
                data={counts}
                getRowKey={(row) => row.label}
                columns={[
                  {
                    id: 'metric',
                    header: t('Metric'),
                    cell: (row) => row.label,
                  },
                  {
                    id: 'count',
                    header: t('Observed count'),
                    cell: (row) => row.value,
                  },
                ]}
              />
            </div>
            <div className='min-w-0 space-y-2'>
              <h3 className='text-sm font-medium'>
                {t('Uncertain or missing observations')}
              </h3>
              <StaticDataTable
                data={uncertainty}
                getRowKey={(row) => row.label}
                columns={[
                  {
                    id: 'metric',
                    header: t('Metric'),
                    cell: (row) => row.label,
                  },
                  {
                    id: 'count',
                    header: t('Observed count'),
                    cell: (row) =>
                      row.value === undefined
                        ? t('Not collected')
                        : formatNumber(row.value, locale),
                  },
                ]}
              />
            </div>
          </div>
          <div className='space-y-2'>
            <h3 className='text-sm font-medium'>{t('Observation coverage')}</h3>
            <p className='text-muted-foreground text-xs'>
              {t(
                'Expired means an earlier report is no longer current. Unreported includes older clients that have never provided workload data; neither proves a task stopped.'
              )}
            </p>
            <StaticDataTable
              data={collections}
              getRowKey={(row) => row.key}
              columns={[
                {
                  id: 'kind',
                  header: t('Collection'),
                  cell: (row) => row.label,
                },
                {
                  id: 'coverage',
                  header: t('Coverage'),
                  cell: (row) =>
                    labels[data.coverage.collections[row.key].coverage],
                },
                {
                  id: 'runtimes',
                  header: t('Collected processes / online processes'),
                  cell: (row) =>
                    `${formatNumber(data.coverage.collections[row.key].collected_runtime_sessions, locale)} / ${formatNumber(data.coverage.online_runtime_sessions, locale)}`,
                },
              ]}
            />
          </div>
        </>
      )}
    </section>
  )
}
