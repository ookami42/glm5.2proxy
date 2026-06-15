import { useCallback, useEffect, useState } from 'react'
import { AlertTriangle, Info, RefreshCw, XCircle } from 'lucide-react'
import { api } from '@/lib/api'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { ScrollArea } from '@/components/ui/scroll-area'

interface LogEntry {
  id: number
  timestamp: string
  level: 'info' | 'warn' | 'error'
  event: string
  message: string
}

interface LogsDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
}

const levelStyle = {
  info: { icon: Info, className: 'text-sky-400 bg-sky-500/10' },
  warn: { icon: AlertTriangle, className: 'text-amber-400 bg-amber-500/10' },
  error: { icon: XCircle, className: 'text-red-400 bg-red-500/10' },
}

export function LogsDialog({ open, onOpenChange }: LogsDialogProps) {
  const [logs, setLogs] = useState<LogEntry[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const refresh = useCallback(async () => {
    setLoading(true)
    try {
      const response = await api.get<{ data: LogEntry[] }>('/api/admin/logs?limit=250')
      setLogs([...response.data].reverse())
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Falha ao carregar logs')
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    if (!open) return
    void refresh()
    const timer = setInterval(refresh, 3000)
    return () => clearInterval(timer)
  }, [open, refresh])

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[82vh] max-w-3xl">
        <DialogHeader className="flex-row items-center justify-between pr-8">
          <div>
            <DialogTitle>Logs do sistema</DialogTitle>
            <p className="mt-1 text-xs text-muted-foreground">Eventos reais mantidos pelo processo Go</p>
          </div>
          <Button variant="outline" size="sm" onClick={refresh} disabled={loading}>
            <RefreshCw className={loading ? 'h-3.5 w-3.5 animate-spin' : 'h-3.5 w-3.5'} />
            Atualizar
          </Button>
        </DialogHeader>

        <ScrollArea className="h-[58vh] pr-4">
          {error ? (
            <p className="rounded-md border border-red-500/25 bg-red-500/5 p-3 text-sm text-red-400">{error}</p>
          ) : logs.length === 0 ? (
            <p className="py-16 text-center text-sm text-muted-foreground">Nenhum evento registrado nesta execução.</p>
          ) : (
            <div className="space-y-2">
              {logs.map((entry) => {
                const style = levelStyle[entry.level] ?? levelStyle.info
                const Icon = style.icon
                return (
                  <div key={entry.id} className="flex gap-3 rounded-md border border-border/50 bg-background/35 p-3">
                    <div className={`mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded ${style.className}`}>
                      <Icon className="h-3.5 w-3.5" />
                    </div>
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                        <span className="font-mono text-[10px] text-muted-foreground">
                          {new Date(entry.timestamp).toLocaleTimeString('pt-BR')}
                        </span>
                        <span className="font-mono text-[10px] uppercase text-muted-foreground">{entry.event}</span>
                      </div>
                      <p className="mt-1 break-words text-xs leading-relaxed">{entry.message}</p>
                    </div>
                  </div>
                )
              })}
            </div>
          )}
        </ScrollArea>
      </DialogContent>
    </Dialog>
  )
}
