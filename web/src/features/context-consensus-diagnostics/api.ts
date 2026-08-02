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
import { api } from '@/lib/api'

import type {
  ContextConsensusDiagnosticsData,
  ContextConsensusDiagnosticsResponse,
} from './types'

export async function getContextConsensusDiagnostics(
  startTimestamp: number,
  endTimestamp: number
): Promise<ContextConsensusDiagnosticsData> {
  const response = await api.get<ContextConsensusDiagnosticsResponse>(
    '/api/smart-routing/context-consensus/diagnostics',
    {
      params: {
        start_timestamp: startTimestamp,
        end_timestamp: endTimestamp,
      },
      skipBusinessError: true,
      skipErrorHandler: true,
    }
  )
  const source = response.data.data
  return {
    schema_version: source.schema_version,
    start_timestamp: source.start_timestamp,
    end_timestamp: source.end_timestamp,
    data_scope: source.data_scope,
    data_quality: {
      matched_logs: source.data_quality.matched_logs,
      valid_diagnostics: source.data_quality.valid_diagnostics,
      invalid_diagnostics: source.data_quality.invalid_diagnostics,
      legacy_logs: source.data_quality.legacy_logs,
      oversized_logs: source.data_quality.oversized_logs,
    },
    summary: {
      not_applicable: source.summary.not_applicable,
      tool_contexts: source.summary.tool_contexts,
      ready_for_sanitization: source.summary.ready_for_sanitization,
      blocked: source.summary.blocked,
      ready_rate: source.summary.ready_rate,
      reason_occurrences: source.summary.reason_occurrences,
    },
    by_reason_code: source.by_reason_code.map((item) => ({
      reason_code: item.reason_code,
      count: item.count,
    })),
    by_protocol: source.by_protocol.map((item) => ({
      protocol: item.protocol,
      tool_contexts: item.tool_contexts,
      ready_for_sanitization: item.ready_for_sanitization,
      blocked: item.blocked,
    })),
    timeline: source.timeline.map((item) => ({
      bucket_timestamp: item.bucket_timestamp,
      tool_contexts: item.tool_contexts,
      ready_for_sanitization: item.ready_for_sanitization,
      blocked: item.blocked,
    })),
  }
}
