import { useState, useCallback } from 'react'
import { api } from '@/lib/api'
import { openExternalURL } from '@/lib/wails'

interface LoginFlow {
  flowId: string
  authorizeUrl: string
  pollIntervalSec: number
  status: string
}

interface PollResult {
  status: string
  account?: {
    id: string
    email: string
  }
  error?: string
}

export function useAuth() {
  const [pending, setPending] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const startLogin = useCallback(async () => {
    setPending(true)
    setError(null)
    try {
      const flow = await api.post<LoginFlow>('/api/admin/auth/login/start')
      openExternalURL(flow.authorizeUrl)
      return flow
    } catch (err) {
      const msg = err instanceof Error ? err.message : 'Login failed'
      setError(msg)
      setPending(false)
      throw err
    }
  }, [])

  const pollLogin = useCallback(async (flowId: string): Promise<PollResult> => {
    try {
      const result = await api.get<PollResult>(`/api/admin/auth/login/poll?flow_id=${flowId}`)
      if (result.status === 'ready' || result.status === 'failed') {
        setPending(false)
      }
      return result
    } catch (err) {
      const msg = err instanceof Error ? err.message : 'Poll failed'
      setError(msg)
      setPending(false)
      throw err
    }
  }, [])

  return { pending, error, startLogin, pollLogin }
}
