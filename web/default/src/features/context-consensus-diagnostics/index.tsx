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
import { RefreshCw, ShieldCheck } from 'lucide-react'
import { lazy, Suspense, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

import { ErrorState } from '@/components/error-state'
import { Button } from '@/components/ui/button'
import { NativeSelect, NativeSelectOption } from '@/components/ui/native-select'
import { Skeleton } from '@/components/ui/skeleton'
import { cn } from '@/lib/utils'

import { getContextConsensusDiagnostics } from './api'
import { BreakdownTables } from './components/breakdown-tables'
import { SummaryMetrics } from './components/summary-metrics'
import type { ContextConsensusDiagnosticsPeriod } from './types'

const DiagnosticsTimeline = lazy(
  () => import('./components/diagnostics-timeline')
)

const PERIODS = new Set<ContextConsensusDiagnosticsPeriod>([24, 72, 168])
const SUMMARY_SKELETON_KEYS = ['tool-contexts', 'ready', 'blocked', 'other']

type DiagnosticsTimeRange = {
  startTimestamp: number
  endTimestamp: number
}

function createTimeRange(hours: ContextConsensusDiagnosticsPeriod) {
  const endTimestamp = Math.floor(Date.now() / 1000) - 60
  return {
    startTimestamp: endTimestamp - hours * 60 * 60,
    endTimestamp,
  }
}

export function ContextConsensusDiagnostics() {
  const { t, i18n } = useTranslation()
  const [period, setPeriod] = useState<ContextConsensusDiagnosticsPeriod>(24)
  const [timeRange, setTimeRange] = useState<DiagnosticsTimeRange>(() =>
    createTimeRange(24)
  )
  const diagnosticsQuery = useQuery({
    queryKey: [
      'context-consensus-diagnostics',
      timeRange.startTimestamp,
      timeRange.endTimestamp,
    ],
    queryFn: () =>
      getContextConsensusDiagnostics(
        timeRange.startTimestamp,
        timeRange.endTimestamp
      ),
    staleTime: 60_000,
    retry: false,
  })

  const handlePeriodChange = (value: string) => {
    const nextPeriod = Number(value) as ContextConsensusDiagnosticsPeriod
    if (!PERIODS.has(nextPeriod)) return
    setPeriod(nextPeriod)
    setTimeRange(createTimeRange(nextPeriod))
  }
  const refresh = () => {
    const nextRange = createTimeRange(period)
    if (
      nextRange.startTimestamp === timeRange.startTimestamp &&
      nextRange.endTimestamp === timeRange.endTimestamp
    ) {
      void diagnosticsQuery.refetch()
      return
    }
    setTimeRange(nextRange)
  }
  const data = diagnosticsQuery.data
  const dateFormatter = new Intl.DateTimeFormat(i18n.language, {
    dateStyle: 'medium',
    timeStyle: 'short',
  })

  let diagnosticsContent: ReactNode = null
  if (diagnosticsQuery.isLoading) {
    diagnosticsContent = (
      <div className='space-y-4'>
        <div className='grid gap-3 sm:grid-cols-2 xl:grid-cols-4'>
          {SUMMARY_SKELETON_KEYS.map((key) => (
            <Skeleton key={key} className='h-28 rounded-lg' />
          ))}
        </div>
        <Skeleton className='h-72 rounded-lg' />
      </div>
    )
  } else if (diagnosticsQuery.isError) {
    diagnosticsContent = (
      <ErrorState
        title={t('We could not load context diagnostics.')}
        description={t('Try refreshing or choose a shorter reporting period.')}
        onRetry={refresh}
        className='min-h-[320px]'
      />
    )
  } else if (data?.data_quality.matched_logs === 0) {
    diagnosticsContent = (
      <div className='rounded-lg border border-dashed px-4 py-14 text-center'>
        <ShieldCheck
          className='text-muted-foreground mx-auto size-7'
          aria-hidden='true'
        />
        <h3 className='mt-3 text-sm font-medium'>
          {t('No persisted Smart Routing consume logs in this period.')}
        </h3>
        <p className='text-muted-foreground mt-1 text-xs'>
          {t('Choose a longer period or refresh after new requests complete.')}
        </p>
      </div>
    )
  } else if (data) {
    diagnosticsContent = (
      <div className='space-y-4'>
        <SummaryMetrics data={data} />
        <Suspense fallback={<Skeleton className='h-72 rounded-lg' />}>
          <DiagnosticsTimeline data={data} />
        </Suspense>
        <BreakdownTables data={data} />
        <div className='text-muted-foreground flex flex-wrap gap-x-5 gap-y-1 border-t pt-3 text-xs'>
          <span>
            {t('Matched logs')}: {data.data_quality.matched_logs}
          </span>
          <span>
            {t('Valid diagnostics')}: {data.data_quality.valid_diagnostics}
          </span>
          <span>
            {t('Legacy logs')}: {data.data_quality.legacy_logs}
          </span>
          <span>
            {t('Invalid diagnostics')}: {data.data_quality.invalid_diagnostics}
          </span>
          <span>
            {t('Oversized logs')}: {data.data_quality.oversized_logs}
          </span>
        </div>
      </div>
    )
  }

  return (
    <div className='min-w-0 space-y-4' aria-busy={diagnosticsQuery.isFetching}>
      <header className='flex flex-col gap-3 border-b pb-4 lg:flex-row lg:items-end lg:justify-between'>
        <div className='min-w-0'>
          <div className='flex items-center gap-2'>
            <ShieldCheck
              className='text-primary size-4 shrink-0'
              aria-hidden='true'
            />
            <h2 className='text-sm font-semibold'>
              {t('Tool context structural eligibility')}
            </h2>
          </div>
          <p className='text-muted-foreground mt-1 max-w-3xl text-xs leading-5'>
            {t(
              'Aggregated diagnostics from persisted successful Smart Routing consume logs. Structural readiness does not mean a sanitization policy is registered or compaction occurred.'
            )}
          </p>
          <p className='text-muted-foreground mt-1 font-mono text-[11px] tabular-nums'>
            {dateFormatter.format(timeRange.startTimestamp * 1000)} -{' '}
            {dateFormatter.format(timeRange.endTimestamp * 1000)}
          </p>
        </div>
        <div className='flex shrink-0 items-center gap-2'>
          <NativeSelect
            size='sm'
            value={String(period)}
            onChange={(event) => handlePeriodChange(event.target.value)}
            aria-label={t('Time range')}
          >
            <NativeSelectOption value='24'>
              {t('Last 24 hours')}
            </NativeSelectOption>
            <NativeSelectOption value='72'>
              {t('Last 3 days')}
            </NativeSelectOption>
            <NativeSelectOption value='168'>
              {t('Last 7 days')}
            </NativeSelectOption>
          </NativeSelect>
          <Button
            type='button'
            variant='outline'
            size='sm'
            onClick={refresh}
            disabled={diagnosticsQuery.isFetching}
            aria-label={t('Refresh')}
          >
            <RefreshCw
              className={cn(
                'size-3.5',
                diagnosticsQuery.isFetching && 'animate-spin'
              )}
              aria-hidden='true'
            />
            {t('Refresh')}
          </Button>
        </div>
      </header>

      {diagnosticsContent}
    </div>
  )
}
