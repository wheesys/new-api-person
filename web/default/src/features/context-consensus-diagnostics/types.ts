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
export type ContextConsensusDiagnosticsPeriod = 24 | 72 | 168

export type ContextConsensusDiagnosticsData = {
  schema_version: number
  start_timestamp: number
  end_timestamp: number
  data_scope: 'successful_smart_routing_consume_logs'
  data_quality: {
    matched_logs: number
    valid_diagnostics: number
    invalid_diagnostics: number
    legacy_logs: number
    oversized_logs: number
  }
  summary: {
    not_applicable: number
    tool_contexts: number
    ready_for_sanitization: number
    blocked: number
    ready_rate: number
    reason_occurrences: number
  }
  by_reason_code: Array<{
    reason_code: string
    count: number
  }>
  by_protocol: Array<{
    protocol: string
    tool_contexts: number
    ready_for_sanitization: number
    blocked: number
  }>
  timeline: Array<{
    bucket_timestamp: number
    tool_contexts: number
    ready_for_sanitization: number
    blocked: number
  }>
}

export type ContextConsensusDiagnosticsResponse = {
  success: boolean
  data: ContextConsensusDiagnosticsData
}
