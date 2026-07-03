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
import { ChevronDown } from 'lucide-react'
import type { UseFormReturn } from 'react-hook-form'
import { useTranslation } from 'react-i18next'

import { SideDrawerSection } from '@/components/drawer-layout'
import { Button } from '@/components/ui/button'
import {
  Collapsible,
  CollapsibleContent,
  CollapsibleTrigger,
} from '@/components/ui/collapsible'
import {
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { RadioGroup, RadioGroupItem } from '@/components/ui/radio-group'

import type {
  ExtendedModelFormValues,
  PricingMode,
  PricingSubMode,
} from './model-mutate-drawer-types'

type ModelPricingFormSectionProps = {
  form: UseFormReturn<ExtendedModelFormValues>
  pricingMode: PricingMode
  onPricingModeChange: (mode: PricingMode) => void
  pricingSubMode: PricingSubMode
  onPricingSubModeChange: (mode: PricingSubMode) => void
  advancedOpen: boolean
  onAdvancedOpenChange: (open: boolean) => void
  promptPrice: string
  completionPrice: string
  onPromptPriceChange: (value: string) => void
  onCompletionPriceChange: (value: string) => void
  onPromptPricePreviewChange: (value: string) => void
  onCompletionPricePreviewChange: (value: string) => void
  validateNumber: (value: string) => boolean
}

function isNumericText(value?: string) {
  return Boolean(value) && !Number.isNaN(Number.parseFloat(value ?? ''))
}

export function ModelPricingFormSection({
  form,
  pricingMode,
  onPricingModeChange,
  pricingSubMode,
  onPricingSubModeChange,
  advancedOpen,
  onAdvancedOpenChange,
  promptPrice,
  completionPrice,
  onPromptPriceChange,
  onCompletionPriceChange,
  onPromptPricePreviewChange,
  onCompletionPricePreviewChange,
  validateNumber,
}: ModelPricingFormSectionProps) {
  const { t } = useTranslation()

  return (
    <SideDrawerSection>
      <h3 className='text-sm font-semibold'>{t('Pricing Configuration')}</h3>

      <div className='space-y-4'>
        <Label>{t('Pricing mode')}</Label>
        <RadioGroup
          value={pricingMode}
          onValueChange={(value) => onPricingModeChange(value as PricingMode)}
        >
          <div className='flex items-center space-x-2'>
            <RadioGroupItem value='per-token' id='per-token' />
            <Label htmlFor='per-token' className='font-normal'>
              {t('Per-token (ratio based)')}
            </Label>
          </div>
          <div className='flex items-center space-x-2'>
            <RadioGroupItem value='per-request' id='per-request' />
            <Label htmlFor='per-request' className='font-normal'>
              {t('Per-request (fixed price)')}
            </Label>
          </div>
        </RadioGroup>
      </div>

      {pricingMode === 'per-request' ? (
        <FormField
          control={form.control}
          name='price'
          render={({ field }) => (
            <FormItem>
              <FormLabel>{t('Fixed price (USD)')}</FormLabel>
              <FormControl>
                <Input
                  type='text'
                  placeholder='0.01'
                  {...field}
                  onChange={(event) => {
                    const value = event.target.value
                    if (validateNumber(value)) {
                      field.onChange(value)
                    }
                  }}
                />
              </FormControl>
              <FormDescription>
                {t('Cost in USD per request, regardless of tokens used.')}
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />
      ) : (
        <>
          <div className='space-y-4'>
            <Label>{t('Input mode')}</Label>
            <RadioGroup
              value={pricingSubMode}
              onValueChange={(value) =>
                onPricingSubModeChange(value as PricingSubMode)
              }
            >
              <div className='flex items-center space-x-2'>
                <RadioGroupItem value='ratio' id='ratio' />
                <Label htmlFor='ratio' className='font-normal'>
                  {t('Ratio mode')}
                </Label>
              </div>
              <div className='flex items-center space-x-2'>
                <RadioGroupItem value='price' id='price' />
                <Label htmlFor='price' className='font-normal'>
                  {t('Price mode (USD per 1M tokens)')}
                </Label>
              </div>
            </RadioGroup>
          </div>

          {pricingSubMode === 'ratio' ? (
            <>
              <FormField
                control={form.control}
                name='ratio'
                render={({ field }) => {
                  let description = t('Multiplier for prompt tokens.')
                  if (isNumericText(field.value)) {
                    description = `Calculated price: $${(
                      Number.parseFloat(field.value || '') * 2
                    ).toFixed(4)} per 1M tokens`
                  }

                  return (
                    <FormItem>
                      <FormLabel>{t('Model ratio')}</FormLabel>
                      <FormControl>
                        <Input
                          type='text'
                          placeholder='1.0'
                          {...field}
                          onChange={(event) => {
                            const value = event.target.value
                            if (validateNumber(value)) {
                              field.onChange(value)
                              if (value) {
                                onPromptPricePreviewChange(
                                  (Number.parseFloat(value) * 2).toString()
                                )
                              } else {
                                onPromptPricePreviewChange('')
                              }
                            }
                          }}
                        />
                      </FormControl>
                      <FormDescription>{description}</FormDescription>
                      <FormMessage />
                    </FormItem>
                  )
                }}
              />

              <FormField
                control={form.control}
                name='completionRatio'
                render={({ field }) => {
                  let description = t('Multiplier for completion tokens.')
                  if (
                    isNumericText(field.value) &&
                    isNumericText(promptPrice)
                  ) {
                    description = `Calculated price: $${(
                      Number.parseFloat(promptPrice) *
                      Number.parseFloat(field.value || '')
                    ).toFixed(4)} per 1M tokens`
                  }

                  return (
                    <FormItem>
                      <FormLabel>{t('Completion ratio')}</FormLabel>
                      <FormControl>
                        <Input
                          type='text'
                          placeholder='1.0'
                          {...field}
                          onChange={(event) => {
                            const value = event.target.value
                            if (validateNumber(value)) {
                              field.onChange(value)
                              const ratio = form.getValues('ratio')
                              if (value && ratio) {
                                const calculatedPrice =
                                  Number.parseFloat(ratio) *
                                  2 *
                                  Number.parseFloat(value)
                                onCompletionPricePreviewChange(
                                  calculatedPrice.toString()
                                )
                              } else {
                                onCompletionPricePreviewChange('')
                              }
                            }
                          }}
                        />
                      </FormControl>
                      <FormDescription>{description}</FormDescription>
                      <FormMessage />
                    </FormItem>
                  )
                }}
              />
            </>
          ) : (
            <div className='space-y-4'>
              <div className='space-y-2'>
                <Label>{t('Prompt price ($/1M tokens)')}</Label>
                <Input
                  type='text'
                  placeholder='2.0'
                  value={promptPrice}
                  onChange={(event) => onPromptPriceChange(event.target.value)}
                />
                <p className='text-muted-foreground text-sm'>
                  {isNumericText(promptPrice)
                    ? `Calculated ratio: ${(Number.parseFloat(promptPrice) / 2).toFixed(4)}`
                    : t('Enter Input price to calculate ratio')}
                </p>
              </div>

              <div className='space-y-2'>
                <Label>{t('Completion price ($/1M tokens)')}</Label>
                <Input
                  type='text'
                  placeholder='4.0'
                  value={completionPrice}
                  onChange={(event) =>
                    onCompletionPriceChange(event.target.value)
                  }
                />
                <p className='text-muted-foreground text-sm'>
                  {isNumericText(completionPrice) &&
                  isNumericText(promptPrice) &&
                  Number.parseFloat(promptPrice) > 0
                    ? `Calculated ratio: ${(Number.parseFloat(completionPrice) / Number.parseFloat(promptPrice)).toFixed(4)}`
                    : t('Enter Completion price to calculate ratio')}
                </p>
              </div>
            </div>
          )}

          <Collapsible open={advancedOpen} onOpenChange={onAdvancedOpenChange}>
            <CollapsibleTrigger
              render={
                <Button
                  type='button'
                  variant='outline'
                  className='flex w-full items-center justify-between'
                />
              }
            >
              {t('Advanced options')}
              <ChevronDown
                className={`h-4 w-4 transition-transform duration-200 ${
                  advancedOpen ? 'rotate-180' : ''
                }`}
              />
            </CollapsibleTrigger>
            <CollapsibleContent className='flex flex-col gap-4 pt-4'>
              <ModelRatioField
                form={form}
                name='cacheRatio'
                label={t('Cache ratio')}
                placeholder='0.1'
                description={t('Discount ratio for cache hits.')}
                validateNumber={validateNumber}
              />
              <ModelRatioField
                form={form}
                name='imageRatio'
                label={t('Image ratio')}
                placeholder='1.0'
                description={t('Multiplier for image processing.')}
                validateNumber={validateNumber}
              />
              <ModelRatioField
                form={form}
                name='audioRatio'
                label={t('Audio ratio')}
                placeholder='1.0'
                description={t('Multiplier for audio inputs.')}
                validateNumber={validateNumber}
              />
              <ModelRatioField
                form={form}
                name='audioCompletionRatio'
                label={t('Audio completion ratio')}
                placeholder='1.0'
                description={t('Multiplier for audio outputs.')}
                validateNumber={validateNumber}
              />
            </CollapsibleContent>
          </Collapsible>
        </>
      )}
    </SideDrawerSection>
  )
}

type ModelRatioFieldProps = {
  form: UseFormReturn<ExtendedModelFormValues>
  name: 'cacheRatio' | 'imageRatio' | 'audioRatio' | 'audioCompletionRatio'
  label: string
  placeholder: string
  description: string
  validateNumber: (value: string) => boolean
}

function ModelRatioField({
  form,
  name,
  label,
  placeholder,
  description,
  validateNumber,
}: ModelRatioFieldProps) {
  return (
    <FormField
      control={form.control}
      name={name}
      render={({ field }) => (
        <FormItem>
          <FormLabel>{label}</FormLabel>
          <FormControl>
            <Input
              type='text'
              placeholder={placeholder}
              {...field}
              onChange={(event) => {
                const value = event.target.value
                if (validateNumber(value)) {
                  field.onChange(value)
                }
              }}
            />
          </FormControl>
          <FormDescription>{description}</FormDescription>
          <FormMessage />
        </FormItem>
      )}
    />
  )
}
