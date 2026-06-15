import { useEffect, useState } from 'react'
import { Check, Copy, KeyRound, Play, Power, Server, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import type { Settings } from '@/types/api'

interface APISettingsDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  settings: Settings
  onToggleAPI: (enabled: boolean) => Promise<void>
  onUpdatePort: (port: number) => Promise<boolean>
  onCreateKey: (name: string) => Promise<string>
  onDeleteKey: (id: string) => Promise<void>
}

export function APISettingsDialog({
  open,
  onOpenChange,
  settings,
  onToggleAPI,
  onUpdatePort,
  onCreateKey,
  onDeleteKey,
}: APISettingsDialogProps) {
  const [port, setPort] = useState(String(settings.port))
  const [keyName, setKeyName] = useState('')
  const [newSecret, setNewSecret] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  const [copied, setCopied] = useState(false)
  const [copiedExample, setCopiedExample] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)
  const baseURL = `http://127.0.0.1:${settings.port}/v1`
  const exampleModel = 'glm-5.2'
  const configuredKey = newSecret ?? (settings.apiKeys[0] ? `${settings.apiKeys[0].prefix}...` : 'qualquer-valor')
  const keyExplanation = newSecret
    ? 'Usando a chave recém-criada abaixo.'
    : settings.apiKeys[0]
      ? 'A chave completa só aparece na criação; use o segredo salvo no seu cliente.'
      : 'Sem API key cadastrada; por enquanto qualquer valor é aceito.'
  const clientConfig = [
    `Base URL: ${baseURL}`,
    `Model: ${exampleModel}`,
    `API key: ${configuredKey}`,
  ].join('\n')
  const curlExample = [
    'curl.exe http://127.0.0.1:' + settings.port + '/v1/chat/completions ^',
    '  -H "Content-Type: application/json" ^',
    '  -H "Authorization: Bearer ' + configuredKey + '" ^',
    '  -d "{\\"model\\":\\"' + exampleModel + '\\",\\"messages\\":[{\\"role\\":\\"user\\",\\"content\\":\\"responda apenas ok\\"}],\\"stream\\":false}"',
  ].join('\n')

  useEffect(() => setPort(String(settings.port)), [settings.port])

  const toggleAPI = async () => {
    setBusy(true)
    try {
      await onToggleAPI(!settings.apiEnabled)
    } finally {
      setBusy(false)
    }
  }

  const savePort = async () => {
    const value = Number(port)
    if (!Number.isInteger(value) || value < 1 || value > 65535) {
      setNotice('A porta deve estar entre 1 e 65535.')
      return
    }
    const restartRequired = await onUpdatePort(value)
    setNotice(restartRequired ? 'Porta salva. Reinicie o aplicativo para aplicá-la.' : 'Porta atualizada.')
  }

  const createKey = async () => {
    setBusy(true)
    try {
      const secret = await onCreateKey(keyName)
      setNewSecret(secret)
      setKeyName('')
      setNotice('A chave é exibida apenas agora. Guarde-a antes de fechar.')
    } finally {
      setBusy(false)
    }
  }

  const copySecret = async () => {
    if (!newSecret) return
    await navigator.clipboard.writeText(newSecret)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }

  const copyClientConfig = async () => {
    await navigator.clipboard.writeText(`${clientConfig}\n\n${curlExample}`)
    setCopiedExample(true)
    setTimeout(() => setCopiedExample(false), 1500)
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[86vh] max-w-2xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>Configuração da API</DialogTitle>
          <DialogDescription>Controle o endpoint OpenAI-compatible, a porta e as credenciais dos clientes.</DialogDescription>
        </DialogHeader>

        <section className="rounded-lg border border-border/60 bg-background/35 p-4">
          <div className="flex items-center justify-between gap-4">
            <div className="flex items-center gap-3">
              <div className={`flex h-9 w-9 items-center justify-center rounded-md ${settings.apiEnabled ? 'bg-emerald-500/12 text-emerald-500' : 'bg-muted text-muted-foreground'}`}>
                <Server className="h-4 w-4" />
              </div>
              <div>
                <p className="text-sm font-semibold">Servidor da API</p>
                <p className="text-xs text-muted-foreground">
                  {settings.apiEnabled ? `Aceitando clientes em 127.0.0.1:${settings.port}` : 'Rotas /v1 desativadas'}
                </p>
              </div>
            </div>
            <Button variant={settings.apiEnabled ? 'outline' : 'default'} onClick={toggleAPI} disabled={busy}>
              {settings.apiEnabled ? <Power className="h-4 w-4" /> : <Play className="h-4 w-4" />}
              {settings.apiEnabled ? 'Parar API' : 'Iniciar API'}
            </Button>
          </div>

          <div className="mt-4 flex items-end gap-2 border-t border-border/50 pt-4">
            <label className="flex-1">
              <span className="mb-1.5 block text-xs font-medium">Porta</span>
              <input
                value={port}
                onChange={(event) => setPort(event.target.value)}
                inputMode="numeric"
                className="h-9 w-full rounded-md border border-input bg-background px-3 text-sm outline-none focus:ring-2 focus:ring-ring"
              />
            </label>
            <Button variant="outline" onClick={savePort}>Salvar porta</Button>
          </div>
        </section>

        <section className="rounded-lg border border-border/60 bg-background/35 p-4">
          <div className="mb-3 flex items-center justify-between gap-3">
            <div>
              <h3 className="text-sm font-semibold">Exemplo para clientes OpenAI-compatible</h3>
              <p className="mt-1 text-xs text-muted-foreground">{keyExplanation}</p>
            </div>
            <Button variant="outline" size="sm" onClick={copyClientConfig}>
              {copiedExample ? <Check className="h-3.5 w-3.5 text-emerald-500" /> : <Copy className="h-3.5 w-3.5" />}
              Copiar
            </Button>
          </div>

          <div className="grid gap-3 md:grid-cols-2">
            <div className="rounded-md border border-border/50 bg-black/20 p-3">
              <p className="mb-2 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
                Kilo, Roo, OpenCode, Codex
              </p>
              <pre className="whitespace-pre-wrap font-mono text-xs leading-relaxed text-foreground">{clientConfig}</pre>
            </div>
            <div className="rounded-md border border-border/50 bg-black/20 p-3">
              <p className="mb-2 text-[11px] font-semibold uppercase tracking-wide text-muted-foreground">
                Teste rapido
              </p>
              <pre className="max-h-32 overflow-auto whitespace-pre-wrap font-mono text-[11px] leading-relaxed text-foreground">{curlExample}</pre>
            </div>
          </div>
        </section>

        <section>
          <div className="mb-3 flex items-center gap-2">
            <KeyRound className="h-4 w-4" />
            <h3 className="text-sm font-semibold">API keys</h3>
            <span className="text-xs text-muted-foreground">({settings.apiKeys.length})</span>
          </div>

          <div className="flex gap-2">
            <input
              value={keyName}
              onChange={(event) => setKeyName(event.target.value)}
              placeholder="Nome, por exemplo: Roo Code"
              className="h-9 flex-1 rounded-md border border-input bg-background px-3 text-sm outline-none focus:ring-2 focus:ring-ring"
            />
            <Button onClick={createKey} disabled={busy}>Criar chave</Button>
          </div>

          {newSecret && (
            <div className="mt-3 rounded-md border border-emerald-500/30 bg-emerald-500/5 p-3">
              <p className="mb-2 text-xs font-semibold text-emerald-500">Nova chave criada</p>
              <div className="flex gap-2">
                <code className="min-w-0 flex-1 select-text overflow-x-auto rounded bg-black/20 px-2.5 py-2 text-xs">{newSecret}</code>
                <Button variant="outline" size="icon" title="Copiar chave" onClick={copySecret}>
                  {copied ? <Check className="h-4 w-4 text-emerald-500" /> : <Copy className="h-4 w-4" />}
                </Button>
              </div>
            </div>
          )}

          <div className="mt-3 space-y-2">
            {settings.apiKeys.length === 0 ? (
              <p className="rounded-md border border-dashed border-border/70 py-6 text-center text-xs text-muted-foreground">
                Sem chave configurada. Enquanto isso, qualquer valor de API key é aceito.
              </p>
            ) : settings.apiKeys.map((key) => (
              <div key={key.id} className="flex items-center justify-between rounded-md border border-border/60 px-3 py-2.5">
                <div>
                  <p className="text-sm font-medium">{key.name}</p>
                  <p className="font-mono text-[11px] text-muted-foreground">{key.prefix}••••••••</p>
                </div>
                <Button variant="ghost" size="icon" title="Excluir API key" onClick={() => onDeleteKey(key.id)}>
                  <Trash2 className="h-4 w-4 text-red-400" />
                </Button>
              </div>
            ))}
          </div>
        </section>

        {notice && <p className="rounded-md bg-muted px-3 py-2 text-xs text-muted-foreground">{notice}</p>}
      </DialogContent>
    </Dialog>
  )
}
