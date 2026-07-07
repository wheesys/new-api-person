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
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip'
import { formatQuota } from '@/lib/format'

type UserQuotaCellProps = {
  used: number
  remaining: number
  requestCount: number
}

export function UserQuotaCell(props: UserQuotaCellProps) {
  const { t } = useTranslation()
  const total = props.used + props.remaining
  const formattedUsed = formatQuota(props.used)
  const formattedRemaining = formatQuota(props.remaining)
  const formattedTotal = formatQuota(total)

  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <div className='w-full min-w-0 cursor-help space-y-0.5 overflow-hidden' />
        }
      >
        <div className='min-w-0 truncate font-medium tabular-nums'>
          {formattedUsed}
        </div>
        <div className='text-muted-foreground min-w-0 truncate text-xs tabular-nums'>
          {t('Requests:')} {props.requestCount.toLocaleString()}
        </div>
      </TooltipTrigger>
      <TooltipContent>
        <div className='space-y-1 text-xs'>
          <div>
            {t('Used:')} {formattedUsed}
          </div>
          <div>
            {t('Remaining:')} {formattedRemaining}
          </div>
          <div>
            {t('Total:')} {formattedTotal}
          </div>
          <div>
            {t('Requests:')} {props.requestCount.toLocaleString()}
          </div>
        </div>
      </TooltipContent>
    </Tooltip>
  )
}
