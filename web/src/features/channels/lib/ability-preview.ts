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
import type { Channel } from '../types'

export type AbilityPreviewModel = {
  requestedModel: string
  upstreamModel: string
  mapped: boolean
}

export type ChannelAbilityPreview = {
  models: AbilityPreviewModel[]
  groups: string[]
  abilityCount: number
}

function uniqueCommaSeparatedValues(value: string): string[] {
  const seen = new Set<string>()
  const values: string[] = []

  for (const item of value.split(',')) {
    const normalized = item.trim()
    if (!normalized || seen.has(normalized)) continue
    seen.add(normalized)
    values.push(normalized)
  }

  return values
}

function parseModelMapping(
  value: string | null | undefined
): Map<string, string> {
  if (!value?.trim()) return new Map()

  try {
    const parsed: unknown = JSON.parse(value)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      return new Map()
    }

    const mapping = new Map<string, string>()
    for (const [requestedModel, upstreamModel] of Object.entries(parsed)) {
      if (typeof upstreamModel !== 'string') continue
      const normalizedRequestedModel = requestedModel.trim()
      const normalizedUpstreamModel = upstreamModel.trim()
      if (!normalizedRequestedModel || !normalizedUpstreamModel) continue
      mapping.set(normalizedRequestedModel, normalizedUpstreamModel)
    }
    return mapping
  } catch {
    return new Map()
  }
}

export function buildChannelAbilityPreview(
  channel: Pick<Channel, 'models' | 'group' | 'model_mapping'>
): ChannelAbilityPreview {
  const requestedModels = uniqueCommaSeparatedValues(channel.models)
  const groups = uniqueCommaSeparatedValues(channel.group)
  const modelMapping = parseModelMapping(channel.model_mapping)
  const models = requestedModels.map((requestedModel) => {
    const mappedModel = modelMapping.get(requestedModel)
    return {
      requestedModel,
      upstreamModel: mappedModel || requestedModel,
      mapped: Boolean(mappedModel && mappedModel !== requestedModel),
    }
  })

  return {
    models,
    groups,
    abilityCount: models.length * groups.length,
  }
}
