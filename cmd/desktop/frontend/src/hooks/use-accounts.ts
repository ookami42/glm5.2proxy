import { useEffect, useState } from 'react'
import { api } from '@/lib/api'
import type { Account, AccountsResponse } from '@/types/api'

export function useAccounts(pollInterval = 5000) {
  const [data, setData] = useState<AccountsResponse | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)

  const refresh = async () => {
    try {
      const res = await api.get<AccountsResponse>('/api/admin/accounts')
      setData(res)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Unknown error')
    } finally {
      setLoading(false)
    }
  }

  const reorder = async (accounts: Account[]) => {
    const previous = data
    const ordered = accounts.map((account, index) => ({
      ...account,
      queuePosition: index + 1,
      registrationOrder: index + 1,
      label: `Conta ${index + 1}`,
    }))
    setData((current) => current ? { ...current, data: ordered } : current)
    try {
      await api.put('/api/admin/accounts/order', {
        accountIds: ordered.map((account) => account.id),
      })
      setError(null)
    } catch (err) {
      setData(previous)
      setError(err instanceof Error ? err.message : 'Falha ao salvar a ordem')
      throw err
    }
  }

  useEffect(() => {
    refresh()
    const id = setInterval(refresh, pollInterval)
    return () => clearInterval(id)
  }, [pollInterval])

  return { data, loading, error, refresh, reorder }
}
