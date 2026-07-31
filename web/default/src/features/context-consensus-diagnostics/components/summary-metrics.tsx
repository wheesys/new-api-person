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
  CircleCheck,
  CircleOff,
  ListFilter,
  ShieldAlert,
  type LucideIcon,
} from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { cn } from '@/lib/utils'

import type { ContextConsensusDiagnosticsData } from '../types'

type SummaryMetric = {
  label: string
  value: string
  detail: string
  icon: LucideIcon
  accent: string
}

type SummaryMetricsProps = {
  data: ContextConsensusDiagnosticsData
}

export function SummaryMetrics(props: SummaryMetricsProps) {
  const { t } = useTranslation()
  const numberFormat = new Intl.NumberFormat()
  const percentFormat = new Intl.NumberFormat(undefined, {
    style: 'percent',
    maximumFractionDigits: 1,
  })
  const summary = props.data.summary
  const metrics: SummaryMetric[] = [
    {
      label: t('Tool contexts'),
      value: numberFormat.format(summary.tool_contexts),
      detail: t('Evaluated from persisted consume logs'),
      icon: ListFilter,
      accent: 'text-sky-600 dark:text-sky-400',
    },
    {
      label: t('Structurally ready'),
      value: numberFormat.format(summary.ready_for_sanitization),
      detail: percentFormat.format(summary.ready_rate),
      icon: CircleCheck,
      accent: 'text-emerald-600 dark:text-emerald-400',
    },
    {
      label: t('Structurally blocked'),
      value: numberFormat.format(summary.blocked),
      detail: t('{{count}} reason occurrences', {
        count: summary.reason_occurrences,
      }),
      icon: ShieldAlert,
      accent: 'text-amber-600 dark:text-amber-400',
    },
    {
      label: t('Not applicable'),
      value: numberFormat.format(summary.not_applicable),
      detail: t('Requests without tool context'),
      icon: CircleOff,
      accent: 'text-muted-foreground',
    },
  ]

  return (
    <div className='grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4'>
      {metrics.map((metric) => {
        const Icon = metric.icon
        return (
          <div
            key={metric.label}
            className='bg-card min-w-0 rounded-lg border px-4 py-3.5 shadow-xs'
          >
            <div className='flex items-start justify-between gap-3'>
              <div className='min-w-0'>
                <p className='text-muted-foreground truncate text-xs font-medium'>
                  {metric.label}
                </p>
                <p className='mt-1 font-mono text-2xl font-semibold tabular-nums'>
                  {metric.value}
                </p>
                <p className='text-muted-foreground mt-1 truncate text-xs'>
                  {metric.detail}
                </p>
              </div>
              <span className='bg-muted inline-flex size-8 shrink-0 items-center justify-center rounded-md'>
                <Icon
                  className={cn('size-4', metric.accent)}
                  aria-hidden='true'
                />
              </span>
            </div>
          </div>
        )
      })}
    </div>
  )
}
