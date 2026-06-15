import { useEffect, useRef, useState } from 'react'
import { Reorder } from 'framer-motion'
import { Activity, KeyRound, ListRestart, Plus, RefreshCw, Server } from 'lucide-react'
import { AccountCard } from '@/components/account-card'
import { APISettingsDialog } from '@/components/api-settings-dialog'
import { LoginDialog } from '@/components/login-dialog'
import { LogsDialog } from '@/components/logs-dialog'
import { ThemeToggle } from '@/components/theme-toggle'
import { Button } from '@/components/ui/button'
import { useAccounts } from '@/hooks/use-accounts'
import { useSettings } from '@/hooks/use-settings'
import { api } from '@/lib/api'
import type { Account } from '@/types/api'

function move(items: Account[], from: number, to: number): Account[] {
  const next = [...items]
  const [item] = next.splice(from, 1)
  if (item) next.splice(to, 0, item)
  return next
}

export function Home() {
  const { data: accountsData, loading, error: accountsError, refresh, reorder } = useAccounts()
  const settingsState = useSettings()
  const [accounts, setAccounts] = useState<Account[]>([])
  const [loginOpen, setLoginOpen] = useState(false)
  const [logsOpen, setLogsOpen] = useState(false)
  const [apiSettingsOpen, setAPISettingsOpen] = useState(false)
  const [refreshing, setRefreshing] = useState(false)
  const dragOrderRef = useRef<Account[]>([])

  useEffect(() => {
    if (accountsData) {
      setAccounts(accountsData.data)
      dragOrderRef.current = accountsData.data
    }
  }, [accountsData])

  const activeAccountId = accountsData?.activeAccountId ?? null
  const settings = settingsState.settings

  const persistOrder = async (ordered: Account[]) => {
    setAccounts(ordered)
    dragOrderRef.current = ordered
    try {
      await reorder(ordered)
      await refresh()
    } catch {
      await refresh()
    }
  }

  const activate = async (id: string) => {
    await api.post(`/api/admin/accounts/${id}/activate`)
    await refresh()
  }

  const refreshAccounts = async () => {
    setRefreshing(true)
    try {
      await refresh()
    } finally {
      setRefreshing(false)
    }
  }

  const moveAccount = async (index: number, direction: -1 | 1) => {
    const target = index + direction
    if (target < 0 || target >= accounts.length) return
    await persistOrder(move(accounts, index, target))
  }

  return (
    <div className="flex h-screen w-full flex-col overflow-hidden bg-background text-foreground">
      <header className="flex h-14 shrink-0 items-center justify-between border-b border-border/50 px-5">
        <div className="flex items-center gap-3">
          <span className={`h-2 w-2 rounded-full ${settings?.apiEnabled ? 'bg-emerald-500 shadow-[0_0_9px_rgba(16,185,129,.8)]' : 'bg-muted-foreground/50'}`} />
          <span className="text-sm font-semibold">glm5.2proxy</span>
          {settings && <span className="text-xs text-muted-foreground">127.0.0.1:{settings.port}</span>}
        </div>
        <div className="flex items-center gap-1.5">
          <Button variant="ghost" size="sm" onClick={() => setLogsOpen(true)}>
            <Activity className="h-4 w-4" /> Logs
          </Button>
          <Button variant="ghost" size="sm" onClick={() => setAPISettingsOpen(true)}>
            <KeyRound className="h-4 w-4" /> API
          </Button>
          <ThemeToggle />
        </div>
      </header>

      <main className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto max-w-5xl space-y-6 px-6 py-6">
          {settings && (
            <section className={`flex items-center justify-between rounded-lg border px-4 py-3 ${settings.apiEnabled ? 'border-emerald-500/30 bg-emerald-500/[.045]' : 'border-border/70 bg-card/50'}`}>
              <div className="flex items-center gap-3">
                <div className={`flex h-9 w-9 items-center justify-center rounded-md ${settings.apiEnabled ? 'bg-emerald-500/12 text-emerald-500' : 'bg-muted text-muted-foreground'}`}>
                  <Server className="h-4 w-4" />
                </div>
                <div>
                  <p className="text-sm font-semibold">{settings.apiEnabled ? 'API OpenAI ativa' : 'API OpenAI parada'}</p>
                  <p className="text-xs text-muted-foreground">
                    {settings.apiEnabled
                      ? `${settings.apiKeys.length || 'Nenhuma'} chave configurada Â· /v1/chat/completions disponÃ­vel`
                      : 'O painel continua funcionando; clientes /v1 recebem API indisponÃ­vel'}
                  </p>
                </div>
              </div>
              <Button
                variant={settings.apiEnabled ? 'outline' : 'default'}
                onClick={() => settingsState.setAPIEnabled(!settings.apiEnabled)}
              >
                {settings.apiEnabled ? 'Parar API' : 'Iniciar API'}
              </Button>
            </section>
          )}

          <section>
            <div className="mb-4 flex items-end justify-between gap-4">
              <div>
                <h1 className="text-2xl font-semibold">Fila de contas</h1>
                <p className="mt-1 text-sm text-muted-foreground">
                  {loading ? 'Consultando contas e cotas...' : `${accounts.length} conta${accounts.length === 1 ? '' : 's'} Â· arraste pelo puxador para reordenar`}
                </p>
              </div>
              <div className="flex gap-2">
                <Button variant="outline" size="sm" onClick={refreshAccounts} disabled={refreshing}>
                  <RefreshCw className={refreshing ? 'h-4 w-4 animate-spin' : 'h-4 w-4'} />
                  Atualizar cotas
                </Button>
                <Button size="sm" onClick={() => setLoginOpen(true)}>
                  <Plus className="h-4 w-4" /> Adicionar conta
                </Button>
              </div>
            </div>

            {accountsError && (
              <p className="mb-3 rounded-md border border-red-500/25 bg-red-500/5 px-3 py-2 text-xs text-red-400">{accountsError}</p>
            )}

            {accounts.length === 0 && !loading ? (
              <div className="rounded-lg border border-dashed border-border/70 bg-card/30 py-16 text-center">
                <ListRestart className="mx-auto h-9 w-9 text-muted-foreground/50" />
                <p className="mt-3 text-sm font-medium">Nenhuma conta salva</p>
                <p className="mt-1 text-xs text-muted-foreground">Adicione uma conta ZCode para iniciar o pool.</p>
              </div>
            ) : (
              <Reorder.Group
                axis="y"
                values={accounts}
                onReorder={(ordered) => {
                  setAccounts(ordered)
                  dragOrderRef.current = ordered
                }}
                className="space-y-3"
              >
                {accounts.map((account, index) => (
                  <AccountCard
                    key={account.id}
                    account={account}
                    isActive={account.id === activeAccountId}
                    isFirst={index === 0}
                    isLast={index === accounts.length - 1}
                    refreshing={refreshing}
                    onActivate={() => activate(account.id)}
                    onMoveUp={() => moveAccount(index, -1)}
                    onMoveDown={() => moveAccount(index, 1)}
                    onRefresh={refreshAccounts}
                    onDragEnd={() => persistOrder(dragOrderRef.current)}
                  />
                ))}
              </Reorder.Group>
            )}
          </section>
        </div>
      </main>

      <LoginDialog open={loginOpen} onOpenChange={setLoginOpen} onSuccess={refreshAccounts} />
      <LogsDialog open={logsOpen} onOpenChange={setLogsOpen} />
      {settings && (
        <APISettingsDialog
          open={apiSettingsOpen}
          onOpenChange={setAPISettingsOpen}
          settings={settings}
          onToggleAPI={async (enabled) => { await settingsState.setAPIEnabled(enabled) }}
          onUpdatePort={async (port) => (await settingsState.updatePort(port)).restartRequired}
          onCreateKey={async (name) => (await settingsState.createAPIKey(name)).secret}
          onDeleteKey={settingsState.deleteAPIKey}
        />
      )}
    </div>
  )
}
