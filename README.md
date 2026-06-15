# glm5.2proxy

Proxy local em Go para usar contas Z.ai/ZCode Start Plan como uma API
OpenAI-compatible. O projeto tem dois modos de uso:

- app desktop Wails com painel React para contas, cotas, fila, logs, porta e API keys;
- servidor headless para rodar apenas a API/proxy.

O app expoe `/v1/chat/completions` e `/v1/models` para clientes compativeis com
OpenAI, traduz as chamadas para o formato usado pelo ZCode e faz rotacao por
conta/modelo quando a cota esgota.

## Funcionalidades

- Painel desktop Wails/React em `cmd/desktop/frontend`.
- Login OAuth ZCode pelo backend.
- Cofre local AES-256-GCM para contas e tokens.
- API keys locais para proteger `/v1/*`.
- Liga/desliga da API OpenAI-compatible sem fechar o painel.
- Reordenacao de contas e ativacao manual.
- Consulta de cota por conta/modelo.
- Rotacao automatica quando a conta ativa esgota.
- Fila local por `conta + modelo` para evitar limite de concorrencia upstream.
- Streaming e non-streaming.
- Tools/function calling, system/developer messages e resultados de tools.
- Captcha headless com Chrome/Edge e fallback interativo.
- Logs administrativos no painel e em `/api/admin/logs`.

## Arquitetura

```text
Cliente OpenAI-compatible
  -> http://127.0.0.1:3005/v1
  -> Go HTTP/SSE proxy
  -> Tradutor OpenAI Chat Completions
  -> ZCode/GLM upstream

App desktop Wails
  -> Painel React
  -> Go bindings + /api/admin/*
  -> Contas, OAuth, cota, fila, API keys, logs e settings
```

O runtime Node antigo nao e mais necessario para o proxy. Node/npm ficam apenas
no frontend do app desktop.

## Executar

Servidor headless:

```powershell
go run ./cmd/server
```

Ou pelo script npm da raiz:

```powershell
npm start
```

App desktop em desenvolvimento:

```powershell
npm run desktop:dev
```

Porta padrao:

```text
http://127.0.0.1:3005
```

## Build

Validacao recomendada:

```powershell
go test ./...
go vet ./...
npm run build --prefix .\cmd\desktop\frontend
```

Servidor Windows:

```powershell
npm run build
.\build\glm5.2proxy-server.exe
```

App desktop Windows:

```powershell
npm run desktop:build
```

Saida principal:

```text
build\bin\glm5.2proxy.exe
```

Servidor Linux amd64 a partir do Windows:

```powershell
$env:CGO_ENABLED='0'
$env:GOOS='linux'
$env:GOARCH='amd64'
go build -trimpath -ldflags='-s -w' `
  -o build\glm5.2proxy-server-linux-amd64 ./cmd/server
Remove-Item Env:GOOS,Env:GOARCH,Env:CGO_ENABLED -ErrorAction SilentlyContinue
```

## Release Local

Os artefatos gerados ficam em:

```text
build\glm5.2proxy-server.exe
build\glm5.2proxy-server-windows-amd64.exe
build\glm5.2proxy-server-linux-amd64
build\bin\glm5.2proxy.exe
release-dist\glm5.2proxy-windows-desktop-amd64.zip
release-dist\glm5.2proxy-windows-server-amd64.zip
release-dist\glm5.2proxy-linux-server-amd64.zip
```

`build/`, `release-dist/`, `cmd/desktop/frontend/dist/` e `node_modules/` sao
artefatos locais e ficam fora do Git pelo `.gitignore`.

## Uso no Windows

1. Extraia `glm5.2proxy-windows-desktop-amd64.zip` em uma pasta fixa.
2. Execute `glm5.2proxy.exe`.
3. No painel, adicione uma conta ZCode.
4. Crie uma API key no painel.
5. Configure seu cliente OpenAI-compatible:

```text
Base URL: http://127.0.0.1:3005/v1
Model: glm-5.2 ou glm-5-turbo
API key: chave criada no painel
```

Dados locais:

```text
C:\Users\<usuario>\.glm5.2proxy
```

Essa pasta contem contas, tokens e configuracoes locais. Nao envie junto com
release ou commit.

## Modelos

| Modelo publico | Upstream | Cota diaria observada |
|---|---|---:|
| `glm-5.2` | `GLM-5.2` | 3.000.000 tokens |
| `glm-5-turbo` | `GLM-5-Turbo` | 2.000.000 tokens |

Capacidades:

```text
GET /api/admin/models/capabilities
GET /v1/models
```

## API OpenAI-Compatible

```text
POST /v1/chat/completions
POST /chat/completions
GET  /v1/models
```

Enquanto nenhuma API key estiver cadastrada, o acesso local pode funcionar sem
segredo. Depois da primeira chave, envie:

```text
Authorization: Bearer zkp_...
```

## APIs Administrativas

Contas e fila:

```text
GET    /api/admin/accounts
GET    /api/admin/accounts/{id}
POST   /api/admin/accounts/{id}/activate
PUT    /api/admin/accounts/order
DELETE /api/admin/accounts/{id}
GET    /api/admin/queue
```

Login:

```text
POST /api/admin/auth/login/start
GET  /api/admin/auth/login/poll?flow_id=...
```

Settings e API keys:

```text
GET    /api/admin/settings
PATCH  /api/admin/settings
GET    /api/admin/api-keys
POST   /api/admin/api-keys
DELETE /api/admin/api-keys/{id}
```

Thinking:

```text
GET /api/admin/thinking
PUT /api/admin/thinking
GET    /api/admin/accounts/{id}/thinking
PUT    /api/admin/accounts/{id}/thinking
DELETE /api/admin/accounts/{id}/thinking
```

Logs:

```text
GET /api/admin/logs?limit=200
```

Captcha:

```text
GET  /zcode/captcha/config
GET  /zcode/captcha/browser
GET  /zcode/captcha/poll
POST /zcode/captcha/submit
POST /zcode/captcha/test
```

Documentacao detalhada da API: [docs/API.md](docs/API.md).

## Estrutura

```text
cmd/server                  servidor headless
main.go                     app desktop Wails
cmd/desktop/frontend        painel React do app desktop
internal/accounts           cofre e contas
internal/accountpool        fila e rotacao
internal/api                APIs HTTP/admin/OpenAI-compatible
internal/app                composicao e lifecycle
internal/auth               OAuth ZCode
internal/captcha            bridge, pagina e navegador
internal/config             configuracao
internal/models             modelos e capacidades
internal/openai             traducao de formatos
internal/proxy              transporte e SSE
internal/quota              billing e porcentagens
internal/requestqueue       serializacao por conta/modelo
internal/state              porta, API keys e thinking
internal/upstream           credenciais e model-io fallback
tests                       testes Go
```

## Testes

```powershell
go test ./...
go vet ./...
npm run build --prefix .\cmd\desktop\frontend
```
