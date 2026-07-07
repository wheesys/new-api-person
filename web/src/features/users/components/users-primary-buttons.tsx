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
import { Eraser, Plus } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { ConfirmDialog } from '@/components/confirm-dialog'
import { Button } from '@/components/ui/button'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { resetAllUsage } from '../api'
import { useUsers } from './users-provider'

export function UsersPrimaryButtons() {
  const { t } = useTranslation()
  const { setOpen, setCurrentRow, triggerRefresh } = useUsers()
  const currentUser = useAuthStore((state) => state.auth.user)
  const [resetUsageOpen, setResetUsageOpen] = useState(false)
  const [resetUsageLoading, setResetUsageLoading] = useState(false)

  const handleCreate = () => {
    setCurrentRow(null)
    setOpen('create')
  }

  const handleResetUsage = async () => {
    setResetUsageLoading(true)
    try {
      const result = await resetAllUsage()
      if (result.success) {
        toast.success(t('Usage data cleared successfully'))
        setResetUsageOpen(false)
        triggerRefresh()
      } else {
        toast.error(result.message || t('Failed to clear usage data'))
      }
    } catch {
      toast.error(t('Failed to clear usage data'))
    } finally {
      setResetUsageLoading(false)
    }
  }

  return (
    <>
      <div className='flex flex-wrap gap-2'>
        {currentUser?.role === ROLE.SUPER_ADMIN && (
          <Button
            size='sm'
            variant='outline'
            className='text-destructive hover:text-destructive'
            onClick={() => setResetUsageOpen(true)}
          >
            <Eraser className='h-4 w-4' />
            {t('Clear Usage')}
          </Button>
        )}
        <Button size='sm' onClick={handleCreate}>
          <Plus className='h-4 w-4' />
          {t('Add User')}
        </Button>
      </div>

      <ConfirmDialog
        open={resetUsageOpen}
        onOpenChange={setResetUsageOpen}
        title={t('Clear all usage data')}
        desc={t(
          'This will reset used quota and request counts for all users and tokens, delete API usage logs, and clear usage statistics. Remaining quota and account settings will not be changed.'
        )}
        confirmText={
          resetUsageLoading ? t('Processing...') : t('Clear Usage')
        }
        destructive
        isLoading={resetUsageLoading}
        handleConfirm={handleResetUsage}
      />
    </>
  )
}
