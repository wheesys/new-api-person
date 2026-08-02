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

import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

import type { ContextConsensusDiagnosticsData } from '../types'

const REASON_LABEL_KEYS: Record<string, string> = {
  tool_compaction_envelope_unavailable: 'Context envelope unavailable',
  tool_compaction_protocol_unsupported: 'Protocol is not supported',
  tool_compaction_provider_bound: 'Provider-bound state is present',
  tool_compaction_media_present: 'Media content is present',
  tool_compaction_schema_missing: 'Tool schema is missing',
  tool_compaction_graph_ambiguous: 'Tool graph is ambiguous',
  tool_compaction_exchange_count_unsupported:
    'Tool exchange count is not supported',
  tool_compaction_exchange_incomplete: 'Tool exchange is incomplete',
  tool_compaction_opaque_state: 'Opaque tool state is present',
  tool_compaction_identity_missing: 'Tool identity is incomplete',
  tool_compaction_digest_missing: 'Structural digest is missing',
  tool_compaction_digest_invalid: 'Structural digest is invalid',
  tool_compaction_sequence_invalid: 'Tool sequence is invalid',
}

const PROTOCOL_LABEL_KEYS: Record<string, string> = {
  openai: 'OpenAI Chat Completions',
  openai_responses: 'OpenAI Responses',
  claude: 'Claude Messages',
  gemini: 'Google Gemini',
}

type BreakdownTablesProps = {
  data: ContextConsensusDiagnosticsData
}

export function BreakdownTables(props: BreakdownTablesProps) {
  const { t } = useTranslation()
  const numberFormat = new Intl.NumberFormat()

  return (
    <div className='grid min-w-0 gap-4 xl:grid-cols-2'>
      <section className='min-w-0 overflow-hidden rounded-lg border'>
        <header className='border-b px-4 py-3'>
          <h3 className='text-sm font-semibold'>{t('Blocking reasons')}</h3>
          <p className='text-muted-foreground mt-0.5 text-xs'>
            {t('One blocked request can contribute more than one reason.')}
          </p>
        </header>
        {props.data.by_reason_code.length === 0 ? (
          <p className='text-muted-foreground px-4 py-8 text-center text-sm'>
            {t('No blocking reasons in this period.')}
          </p>
        ) : (
          <div className='overflow-x-auto'>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('Reason')}</TableHead>
                  <TableHead className='w-28 text-right'>
                    {t('Occurrences')}
                  </TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {props.data.by_reason_code.map((item) => (
                  <TableRow key={item.reason_code}>
                    <TableCell className='min-w-52 text-sm'>
                      {t(
                        REASON_LABEL_KEYS[item.reason_code] ??
                          'Unknown structural restriction'
                      )}
                    </TableCell>
                    <TableCell className='text-right font-mono text-sm tabular-nums'>
                      {numberFormat.format(item.count)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </section>

      <section className='min-w-0 overflow-hidden rounded-lg border'>
        <header className='border-b px-4 py-3'>
          <h3 className='text-sm font-semibold'>{t('Protocol breakdown')}</h3>
          <p className='text-muted-foreground mt-0.5 text-xs'>
            {t('Structural eligibility by supported request protocol.')}
          </p>
        </header>
        {props.data.by_protocol.length === 0 ? (
          <p className='text-muted-foreground px-4 py-8 text-center text-sm'>
            {t('No tool contexts in this period.')}
          </p>
        ) : (
          <div className='overflow-x-auto'>
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>{t('Protocol')}</TableHead>
                  <TableHead className='text-right'>{t('Ready')}</TableHead>
                  <TableHead className='text-right'>{t('Blocked')}</TableHead>
                  <TableHead className='text-right'>{t('Total')}</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {props.data.by_protocol.map((item) => (
                  <TableRow key={item.protocol}>
                    <TableCell className='min-w-44 text-sm'>
                      {t(
                        PROTOCOL_LABEL_KEYS[item.protocol] ??
                          'Unknown request protocol'
                      )}
                    </TableCell>
                    <TableCell className='text-right font-mono text-sm tabular-nums'>
                      {numberFormat.format(item.ready_for_sanitization)}
                    </TableCell>
                    <TableCell className='text-right font-mono text-sm tabular-nums'>
                      {numberFormat.format(item.blocked)}
                    </TableCell>
                    <TableCell className='text-right font-mono text-sm tabular-nums'>
                      {numberFormat.format(item.tool_contexts)}
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        )}
      </section>
    </div>
  )
}
