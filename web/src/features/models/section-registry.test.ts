import assert from 'node:assert/strict'
import { describe, test } from 'node:test'

import type { TFunction } from 'i18next'

import { BILLING_SECTION_IDS } from '../system-settings/billing/section-registry'
import { getModelsSectionNavItems, MODELS_SECTION_IDS } from './section-registry'

const translate = ((key: string) => key) as TFunction

describe('models management navigation sections', () => {
  test('exposes model pricing and group management from model management', () => {
    assert.deepEqual([...MODELS_SECTION_IDS], [
      'metadata',
      'pricing',
      'group-management',
    ])

    assert.deepEqual(
      getModelsSectionNavItems(translate).map((item) => ({
        title: item.title,
        url: item.url,
      })),
      [
        { title: 'Metadata', url: '/models/metadata' },
        { title: 'Pricing', url: '/models/pricing' },
        { title: 'Group Management', url: '/models/group-management' },
      ]
    )
  })

  test('removes group pricing from system settings billing sections', () => {
    assert.equal(
      (BILLING_SECTION_IDS as readonly string[]).includes('group-pricing'),
      false
    )
  })
})
