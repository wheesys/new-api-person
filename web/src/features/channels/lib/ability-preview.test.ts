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
import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import { buildChannelAbilityPreview } from './ability-preview'

describe('buildChannelAbilityPreview', () => {
  test('expands unique models and groups into the backend ability count', () => {
    const preview = buildChannelAbilityPreview({
      models: 'gpt-5, gpt-5-mini,gpt-5',
      group: 'default, premium,default',
      model_mapping: '{"gpt-5":"gpt-5-2026-07-01"}',
    })

    assert.equal(preview.abilityCount, 4)
    assert.deepEqual(preview.groups, ['default', 'premium'])
    assert.deepEqual(preview.models, [
      {
        requestedModel: 'gpt-5',
        upstreamModel: 'gpt-5-2026-07-01',
        mapped: true,
      },
      {
        requestedModel: 'gpt-5-mini',
        upstreamModel: 'gpt-5-mini',
        mapped: false,
      },
    ])
  })

  test('fails closed to direct model names for malformed mappings', () => {
    const preview = buildChannelAbilityPreview({
      models: 'model-a',
      group: 'default',
      model_mapping: '{invalid',
    })

    assert.deepEqual(preview.models, [
      {
        requestedModel: 'model-a',
        upstreamModel: 'model-a',
        mapped: false,
      },
    ])
  })

  test('produces no abilities when either dimension is empty', () => {
    assert.equal(
      buildChannelAbilityPreview({
        models: '',
        group: 'default',
        model_mapping: null,
      }).abilityCount,
      0
    )
    assert.equal(
      buildChannelAbilityPreview({
        models: 'model-a',
        group: '',
        model_mapping: null,
      }).abilityCount,
      0
    )
  })
})
