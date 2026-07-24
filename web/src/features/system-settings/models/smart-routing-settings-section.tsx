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
import { zodResolver } from '@hookform/resolvers/zod'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'
import * as z from 'zod'

import { Button } from '@/components/ui/button'
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Textarea } from '@/components/ui/textarea'

import { SettingsForm } from '../components/settings-form-layout'
import { SettingsPageFormActions } from '../components/settings-page-context'
import { SettingsSection } from '../components/settings-section'
import { useSettingsForm } from '../hooks/use-settings-form'
import { useUpdateOption } from '../hooks/use-update-option'

const supportedVirtualModels = new Set([
  'auto:cheap',
  'auto:balanced',
  'auto:quality',
  'auto:fast',
  'auto:reasoning',
])

const virtualModelPoolsExample = JSON.stringify(
  {
    'auto:cheap': ['gpt-5-mini', 'gemini-2.5-flash'],
    'auto:balanced': ['gpt-5-mini', 'claude-sonnet-4'],
    'auto:quality': ['gpt-5', 'claude-opus-4'],
    'auto:fast': ['gemini-2.5-flash'],
    'auto:reasoning': ['gpt-5', 'o3'],
  },
  null,
  2
)

const virtualModelPoolsSchema = z.string().superRefine((value, ctx) => {
  const trimmed = value.trim()
  if (!trimmed) return

  let parsed: unknown
  try {
    parsed = JSON.parse(trimmed)
  } catch {
    ctx.addIssue({
      code: 'custom',
      message: 'Invalid JSON format',
    })
    return
  }

  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    ctx.addIssue({
      code: 'custom',
      message: 'Expected a JSON object whose values are arrays of model names',
    })
    return
  }

  Object.entries(parsed as Record<string, unknown>).forEach(([key, pool]) => {
    if (!supportedVirtualModels.has(key)) {
      ctx.addIssue({
        code: 'custom',
        message: 'Unsupported virtual model key',
      })
      return
    }

    if (
      !Array.isArray(pool) ||
      !pool.every((modelName) => typeof modelName === 'string')
    ) {
      ctx.addIssue({
        code: 'custom',
        message: 'Each virtual model pool must be an array of model names',
      })
      return
    }
    if (pool.some((modelName) => modelName.trim() === '')) {
      ctx.addIssue({
        code: 'custom',
        message: 'Model name is required',
      })
    }
  })
})

const schema = z.object({
  smart_routing: z.object({
    virtual_model_pools: virtualModelPoolsSchema,
  }),
})

type SmartRoutingSettingsFormInput = z.input<typeof schema>

type SmartRoutingSettingsSectionProps = {
  defaultValues: {
    'smart_routing.virtual_model_pools': string
  }
}

function normalizeVirtualModelPools(value: string) {
  const trimmed = value.trim()
  if (!trimmed) return '{}'
  return JSON.stringify(JSON.parse(trimmed))
}

export function SmartRoutingSettingsSection(
  props: SmartRoutingSettingsSectionProps
) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()
  const defaultValues = {
    smart_routing: {
      virtual_model_pools:
        props.defaultValues['smart_routing.virtual_model_pools'],
    },
  }

  const { form, handleSubmit } = useSettingsForm<SmartRoutingSettingsFormInput>(
    {
      resolver: zodResolver(schema),
      defaultValues,
      compareValues: (left, right) => {
        if (typeof left === 'string' && typeof right === 'string') {
          return (
            normalizeVirtualModelPools(left) ===
            normalizeVirtualModelPools(right)
          )
        }
        return left === right
      },
      onSubmit: async (_values, changedFields) => {
        for (const [key, value] of Object.entries(changedFields)) {
          await updateOption.mutateAsync({
            key,
            value:
              typeof value === 'string'
                ? normalizeVirtualModelPools(value)
                : JSON.stringify(value),
          })
        }
      },
    }
  )

  const formatJsonField = () => {
    const raw = form.getValues('smart_routing.virtual_model_pools')
    if (!raw || !raw.trim()) return
    try {
      const formatted = JSON.stringify(JSON.parse(raw), null, 2)
      form.setValue('smart_routing.virtual_model_pools', formatted, {
        shouldDirty: true,
        shouldValidate: true,
      })
    } catch {
      toast.error(t('Invalid JSON format'))
    }
  }

  return (
    <SettingsSection title={t('Smart Routing')}>
      <Form {...form}>
        <SettingsForm onSubmit={handleSubmit}>
          <SettingsPageFormActions
            onSave={handleSubmit}
            isSaving={updateOption.isPending || form.formState.isSubmitting}
          />

          <FormField
            control={form.control}
            name='smart_routing.virtual_model_pools'
            render={({ field }) => (
              <FormItem>
                <FormLabel>{t('Virtual model pools')}</FormLabel>
                <FormControl>
                  <Textarea
                    rows={14}
                    placeholder={virtualModelPoolsExample}
                    value={field.value}
                    onChange={(event) => field.onChange(event.target.value)}
                  />
                </FormControl>
                <FormDescription>
                  {t(
                    'Configure which real models each Smart Routing virtual model can use.'
                  )}{' '}
                  {t(
                    'Empty object, empty arrays, or missing virtual models keep the existing routing behavior.'
                  )}
                </FormDescription>
                <div className='flex flex-wrap gap-2'>
                  <Button
                    type='button'
                    variant='outline'
                    size='sm'
                    onClick={() => {
                      form.setValue(
                        'smart_routing.virtual_model_pools',
                        virtualModelPoolsExample,
                        { shouldDirty: true, shouldValidate: true }
                      )
                    }}
                  >
                    {t('Fill example')}
                  </Button>
                  <Button
                    type='button'
                    variant='outline'
                    size='sm'
                    onClick={formatJsonField}
                  >
                    {t('Format JSON')}
                  </Button>
                </div>
                <FormMessage />
              </FormItem>
            )}
          />
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
