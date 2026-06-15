import { useEffect, useState } from 'react'
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription } from '@/components/ui/dialog'
import { Button } from '@/components/ui/button'
import { useAuth } from '@/hooks/use-auth'

interface LoginDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSuccess: () => void
}

export function LoginDialog({ open, onOpenChange, onSuccess }: LoginDialogProps) {
  const { pending, error, startLogin, pollLogin } = useAuth()
  const [flowId, setFlowId] = useState<string | null>(null)
  const [status, setStatus] = useState<string>('')

  useEffect(() => {
    if (!open) {
      setFlowId(null)
      setStatus('')
    }
  }, [open])

  useEffect(() => {
    if (!flowId || !pending) return

    const poll = async () => {
      try {
        const result = await pollLogin(flowId)
        setStatus(result.status)
        if (result.status === 'ready') {
          onSuccess()
          onOpenChange(false)
        }
      } catch {
        // The hook exposes the error in the dialog.
      }
    }

    void poll()
    const interval = setInterval(poll, 2000)
    return () => clearInterval(interval)
  }, [flowId, pending, pollLogin, onSuccess, onOpenChange])

  const handleLogin = async () => {
    try {
      const flow = await startLogin()
      if (flow.flowId) {
        setFlowId(flow.flowId)
        setStatus('pending')
      }
    } catch {
      // The hook exposes the error in the dialog.
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Adicionar Conta ZCode</DialogTitle>
          <DialogDescription>
            Autentique com sua conta Google para adicionar ao pool de contas.
          </DialogDescription>
        </DialogHeader>

        <div className="flex flex-col items-center gap-4 py-4">
          {!flowId ? (
            <>
              <Button onClick={handleLogin} disabled={pending} className="w-full" size="lg">
                <svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24">
                  <path fill="#4285F4" d="M22.56 12.25c0-.78-.07-1.53-.2-2.25H12v4.26h5.92c-.26 1.37-1.04 2.53-2.21 3.31v2.77h3.57c2.08-1.92 3.28-4.74 3.28-8.09z"/>
                  <path fill="#34A853" d="M12 23c2.97 0 5.46-.98 7.28-2.66l-3.57-2.77c-.98.66-2.23 1.06-3.71 1.06-2.86 0-5.29-1.93-6.16-4.53H2.18v2.84C3.99 20.53 7.7 23 12 23z"/>
                  <path fill="#FBBC05" d="M5.84 14.09c-.22-.66-.35-1.36-.35-2.09s.13-1.43.35-2.09V7.07H2.18C1.43 8.55 1 10.22 1 12s.43 3.45 1.18 4.93l2.85-2.22.81-.62z"/>
                  <path fill="#EA4335" d="M12 5.38c1.62 0 3.06.56 4.21 1.64l3.15-3.15C17.45 2.09 14.97 1 12 1 7.7 1 3.99 3.47 2.18 7.07l3.66 2.84c.87-2.6 3.3-4.53 6.16-4.53z"/>
                </svg>
                Login com Google
              </Button>
              <p className="text-xs text-muted-foreground text-center">
                Uma janela do navegador será aberta para autenticação.
              </p>
            </>
          ) : (
            <div className="flex flex-col items-center gap-3">
              <div className="h-12 w-12 rounded-full border-4 border-muted border-t-foreground animate-spin" />
              <div className="text-center">
                <p className="text-sm font-medium">Aguardando autenticação...</p>
                <p className="text-xs text-muted-foreground mt-1">
                  {status === 'pending' ? 'Complete o login no navegador' : status}
                </p>
              </div>
            </div>
          )}
          {error && (
            <p role="alert" className="text-sm text-destructive text-center">
              {error}
            </p>
          )}
        </div>
      </DialogContent>
    </Dialog>
  )
}
