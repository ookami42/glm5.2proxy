# GLM 5.2 Proxy — Guia de Estrutura

## Stack
- **Linguagem:** Go 1.24
- **Frontend (desktop):** Wails v2 + React + TypeScript + Vite
- **Infra:** Servidor HTTP puro (sem frameworks), SSE streaming, AES-256-GCM para credenciais

## Estrutura de Diretórios

```
glm5.2proxy/
├── main.go                          # Entrypoint Wails (desktop GUI)
├── cmd/
│   ├── server/main.go               # Entrypoint headless (CLI/server-only)
│   └── desktop/frontend/            # React frontend (Wails)
│       └── src/
│           ├── components/          # UI components
│           │   ├── login-dialog.tsx  # Botão "Login com Google" + polling
│           │   ├── account-card.tsx  # Exibe conta no pool
│           │   ├── home.tsx, onboarding.tsx, settings-dialog.tsx, ...
│           │   └── ui/              # Componentes base (dialog, button, card, etc.)
│           ├── hooks/
│           │   ├── use-auth.ts       # startLogin() + pollLogin() via REST
│           │   ├── use-accounts.ts, use-settings.ts, use-theme.ts
│           ├── lib/
│           │   ├── api.ts            # HTTP client para admin API
│           │   └── wails.ts          # Bridge BrowserOpenURL
│           └── types/api.ts
├── internal/
│   ├── auth/oauth.go                # OAuth CLI flow (Start/Poll)
│   ├── accounts/store.go            # Cofre criptografado de contas
│   ├── accountpool/pool.go          # Seleção/rotação de contas por cota
│   ├── api/
│   │   ├── server.go                # Router HTTP, handlers admin + proxy
│   │   ├── logs.go                  # Buffer circular de logs em memória
│   │   ├── reserve.go               # Rate limiter / reservation
│   │   ├── zcode_bridge.go          # Comandos de live refresh
│   │   └── zcode_environment.go     # Variáveis de ambiente do ZCode
│   ├── app/app.go                   # Composition root (New() → Run())
│   ├── captcha/                     # Bridge de captcha Alibaba Cloud
│   │   ├── bridge.go, browser.go, page.go, config.go, errors.go
│   │   └── process_*.go             # Gerenciamento de processo headless
│   ├── config/config.go             # Config via env vars
│   ├── models/models.go             # Registry de modelos
│   ├── openai/                      # Tradutor OpenAI → Anthropic
│   ├── proxy/
│   │   ├── service.go               # Proxy HTTP/SSE + retry logic
│   │   └── runtime_headers.go       # Injeção de headers (session, captcha)
│   ├── quota/service.go             # Fetch de saldo/billing
│   ├── requestqueue/queue.go        # Fila serializada por conta+modelo
│   ├── state/admin.go               # Config admin (porta, API keys, thinking)
│   └── upstream/config.go           # Headers upstream + body templates
├── tests/                           # Testes unitários
├── docs/                            # Relatórios de análise
├── build/                           # Assets Wails
└── scripts/                         # Scripts auxiliares
```

## Fluxo de Autenticação (ZCode OAuth CLI)

```
[React] click "Login com Google"
  → POST /api/admin/auth/login/start
    → POST https://zcode.z.ai/api/v1/oauth/cli/init {provider: "zai"}
    ← {flow_id, poll_token, authorize_url}
  → openExternalURL(authorize_url)           # Abre navegador
  → Poll a cada 2s GET /api/admin/auth/login/poll?flow_id=...
    → GET https://zcode.z.ai/api/v1/oauth/cli/poll/{flow_id}
    ← {status: "pending" | "ready" | "failed", token, user, zai.access_token}
```

### O que acontece no navegador
1. `authorize_url` → `chat.z.ai/auth/oauth/authorize`
2. Usuário loga com Google (`accounts.google.com`)
3. Google redireciona `chat.z.ai/oauth/google/callback`
4. Tela de consentimento OAuth → usuário clica "Permitir"
5. `chat.z.ai/api/oauth/authorize` (POST) gera authorization code
6. Redirect para `zcode://zai-auth/callback?code=code-XXX`

### Problema conhecido
O passo 6 usa protocolo customizado `zcode://`. Sem um handler registrado, o callback nunca completa. O poll nunca retorna "ready", o flow expira, e o `json.Decode` recebe body vazio → **EOF**.

## Modelo de Dados

```go
// internal/accounts/store.go
Account {
  ID, RegistrationOrder, User{UserID, Email, Name, Avatar},
  ZCodeJWTToken, ZAIAcccessToken, TokenExpiresAt, CreatedAt, UpdatedAt
}

// internal/auth/oauth.go
Flow {
  FlowID, AuthorizeURL, ExpiresAt, PollIntervalSec, Status,
  pollToken (privado)
}

// internal/upstream/config.go
Config {
  Endpoint, BaseHeaders, BodyTemplate, Source, AccountID,
  ActiveAccount, HasAuthorization, HasCaptcha
}
```

## Variáveis de Ambiente Principais

| Variável | Default | Descrição |
|----------|---------|-----------|
| `ZCODE_UPSTREAM_URL` | `https://zcode.z.ai/api/v1/zcode-plan/anthropic/v1/messages` | Upstream chat |
| `ZCODE_OAUTH_BASE_URL` | `https://zcode.z.ai/api/v1` | OAuth API base |
| `ZCODE_PROXY_PORT` | `3005` | Porta do proxy |
| `ZCODE_AUTHORIZATION` | — | Bearer token fixo (desativa pool de contas) |
| `ZCODE_PROXY_CREDENTIAL_SECRET` | machine-derived | Chave AES accounts |
| `ZCODE_CAPTCHA_BRIDGE` | `true` | Bridge de captcha |
| `ZCODE_CAPTCHA_HEADLESS` | `true` | Navegador headless |
| `ZCODE_ACCOUNT_ROTATION` | `true` | Rotação automática |

## Dependências Externas
- **zcode.z.ai** — API upstream (chat, billing, OAuth, telemetria)
- **chat.z.ai** — Frontend Z.ai (login, OAuth consent)
- **accounts.google.com** — Google OAuth
- **Aliyun Captcha** — Desafio captcha no fluxo de chat

## Comandos Úteis

```bash
go build -o proxy ./cmd/server          # Build headless
go test ./tests/...                      # Rodar testes
go test -v ./tests/ -run TestOAuth      # Teste específico
go vet ./...                             # Análise estática
```

## Rotas da API

| Rota | Função |
|------|--------|
| `POST /api/admin/auth/login/start` | Iniciar OAuth flow |
| `GET /api/admin/auth/login/poll` | Polling do flow |
| `GET /api/admin/auth/status` | Status autenticação |
| `GET /api/admin/auth/accounts` | Lista contas |
| `POST /v1/chat/completions` | Proxy chat (OpenAI-compatible) |
| `GET /v1/models` | Lista modelos |
| `GET /api/admin/logs` | Logs do servidor |
