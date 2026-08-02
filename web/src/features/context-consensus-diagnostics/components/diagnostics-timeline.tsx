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
import { VChart } from '@visactor/react-vchart'
import { useMemo } from 'react'
import { useTranslation } from 'react-i18next'

import { useChartTheme } from '@/lib/use-chart-theme'
import { VCHART_OPTION } from '@/lib/vchart'

import type { ContextConsensusDiagnosticsData } from '../types'

type DiagnosticsTimelineProps = {
  data: ContextConsensusDiagnosticsData
}

export default function DiagnosticsTimeline(props: DiagnosticsTimelineProps) {
  const { t, i18n } = useTranslation()
  const { resolvedTheme, themeReady } = useChartTheme()
  const chartTextColor =
    resolvedTheme === 'dark'
      ? 'rgba(255, 255, 255, 0.68)'
      : 'rgba(15, 23, 42, 0.58)'
  const chartGridColor =
    resolvedTheme === 'dark'
      ? 'rgba(255, 255, 255, 0.12)'
      : 'rgba(15, 23, 42, 0.12)'

  const values = useMemo(() => {
    const formatter = new Intl.DateTimeFormat(i18n.language, {
      month: 'short',
      day: 'numeric',
      hour: '2-digit',
    })
    return props.data.timeline.flatMap((point) => {
      const timestamp = point.bucket_timestamp * 1000
      const label = formatter.format(timestamp)
      return [
        {
          timestamp,
          label,
          status: t('Structurally ready'),
          count: point.ready_for_sanitization,
        },
        {
          timestamp,
          label,
          status: t('Structurally blocked'),
          count: point.blocked,
        },
      ]
    })
  }, [i18n.language, props.data.timeline, t])

  const spec = useMemo(
    () => ({
      type: 'bar' as const,
      data: [{ id: 'context-consensus-diagnostics', values }],
      xField: 'label',
      yField: 'count',
      seriesField: 'status',
      stack: true,
      color: ['#10b981', '#f59e0b'],
      legends: {
        visible: true,
        orient: 'top',
        position: 'end',
        item: { label: { style: { fill: chartTextColor, fontSize: 11 } } },
      },
      axes: [
        {
          orient: 'bottom',
          label: {
            style: { fill: chartTextColor, fontSize: 10 },
            autoHide: true,
            autoLimit: true,
          },
          tick: { visible: false },
        },
        {
          orient: 'left',
          label: { style: { fill: chartTextColor, fontSize: 10 } },
          grid: {
            visible: true,
            style: { lineDash: [3, 3], stroke: chartGridColor },
          },
        },
      ],
      tooltip: {
        dimension: {
          title: {
            value: (datum: Record<string, unknown>) =>
              String(datum?.label ?? ''),
          },
          content: [
            {
              key: (datum: Record<string, unknown>) =>
                String(datum?.status ?? ''),
              value: (datum: Record<string, unknown>) =>
                new Intl.NumberFormat(i18n.language).format(
                  Number(datum?.count) || 0
                ),
            },
          ],
        },
      },
      animationAppear: { duration: 350 },
    }),
    [chartGridColor, chartTextColor, i18n.language, values]
  )

  return (
    <section className='min-w-0 overflow-hidden rounded-lg border'>
      <header className='border-b px-4 py-3'>
        <h3 className='text-sm font-semibold'>
          {t('Hourly structural trend')}
        </h3>
        <p className='text-muted-foreground mt-0.5 text-xs'>
          {t('Ready and blocked tool contexts from persisted consume logs.')}
        </p>
      </header>
      <div className='h-64 px-2 py-3 sm:h-72 sm:px-4'>
        {themeReady ? (
          <VChart
            key={`context-consensus-${resolvedTheme}-${i18n.language}`}
            spec={{
              ...spec,
              theme: resolvedTheme === 'dark' ? 'dark' : 'light',
              background: 'transparent',
            }}
            options={VCHART_OPTION}
          />
        ) : null}
      </div>
    </section>
  )
}
