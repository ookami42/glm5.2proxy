package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"glm5.2proxy/internal/accountpool"
	"glm5.2proxy/internal/accounts"
	"glm5.2proxy/internal/auth"
	"glm5.2proxy/internal/captcha"
	"glm5.2proxy/internal/config"
	"glm5.2proxy/internal/models"
	"glm5.2proxy/internal/openai"
	"glm5.2proxy/internal/proxy"
	"glm5.2proxy/internal/quota"
	"glm5.2proxy/internal/requestqueue"
	"glm5.2proxy/internal/state"
	"glm5.2proxy/internal/upstream"
)

type Server struct {
	cfg           config.Config
	port          int
	admin         *state.AdminStore
	accounts      *accounts.Store
	oauth         *auth.Service
	quota         *quota.Service
	pool          *accountpool.Pool
	loader        *upstream.Loader
	captcha       *captcha.Bridge
	browser       *captcha.BrowserManager
	proxy         *proxy.Service
	queue         *requestqueue.Queue
	http          *http.Server
	logs          *logBuffer
	reserve       *usageReserve
	zcode         *zcodeBridge
	zcodeApplyMu  sync.Mutex
	zcodeApplySeq int64
}

const (
	accountListQuotaCacheMaxAge = 30 * time.Second
	accountListQuotaTimeout     = 8 * time.Second
)

func New(
	cfg config.Config,
	port int,
	admin *state.AdminStore,
	accountStore *accounts.Store,
	oauth *auth.Service,
	quotaService *quota.Service,
	pool *accountpool.Pool,
	loader *upstream.Loader,
	bridge *captcha.Bridge,
	browser *captcha.BrowserManager,
	proxyService *proxy.Service,
) *Server {
	server := &Server{cfg: cfg, port: port, admin: admin, accounts: accountStore, oauth: oauth, quota: quotaService, pool: pool, loader: loader, captcha: bridge, browser: browser, proxy: proxyService, queue: requestqueue.New(), logs: newLogBuffer(500), reserve: newUsageReserve(cfg), zcode: newZCodeBridge()}
	server.http = &http.Server{Addr: net.JoinHostPort(cfg.Host, strconv.Itoa(port)), Handler: server.routes(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
	return server
}

func (s *Server) ListenAndServe() error {
	listener, err := s.Listen()
	if err != nil {
		return err
	}
	return s.Serve(listener)
}

func (s *Server) Listen() (net.Listener, error) {
	return net.Listen("tcp", s.http.Addr)
}

func (s *Server) Serve(listener net.Listener) error {
	log.Printf("Go proxy listening on http://%s", s.http.Addr)
	s.logs.add("info", "server.started", "API administrativa e proxy iniciados em http://"+s.http.Addr)
	err := s.http.Serve(listener)
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

func (s *Server) Handler() http.Handler {
	return s.http.Handler
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.health)
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /v1/models", s.requireAPIKey(s.listModels))
	mux.HandleFunc("POST /v1/chat/completions", s.requireAPIKey(s.chat))
	mux.HandleFunc("POST /chat/completions", s.requireAPIKey(s.chat))
	mux.HandleFunc("GET /v1/accounts", s.listAccounts)
	mux.HandleFunc("GET /v1/accounts/{id}", s.getAccount)
	mux.HandleFunc("GET /zcode/accounts", s.listAccounts)
	mux.HandleFunc("GET /zcode/accounts/{id}", s.getAccount)
	mux.HandleFunc("GET /zcode/quota", s.activeQuota)
	mux.HandleFunc("GET /zcode/quota/accounts", s.accountPool)
	mux.HandleFunc("GET /zcode/auth/status", s.authStatus)
	mux.HandleFunc("GET /zcode/auth/accounts", s.authAccounts)
	mux.HandleFunc("POST /zcode/auth/login/start", s.loginStart)
	mux.HandleFunc("GET /zcode/auth/login/poll", s.loginPoll)
	mux.HandleFunc("POST /zcode/auth/accounts/activate", s.activateAccount)
	mux.HandleFunc("DELETE /zcode/auth/accounts", s.deleteAccount)
	mux.HandleFunc("GET /zcode/captcha/poll", s.captcha.Poll)
	mux.HandleFunc("POST /zcode/captcha/submit", s.captcha.Submit)
	mux.HandleFunc("POST /zcode/captcha/test", s.captcha.Test)
	mux.HandleFunc("GET /zcode/captcha/config", s.captchaConfig)
	mux.HandleFunc("GET /zcode/captcha/browser", s.captchaBrowser)

	mux.HandleFunc("GET /api/admin/overview", s.adminOverview)
	mux.HandleFunc("GET /api/admin/settings", s.adminSettings)
	mux.HandleFunc("PATCH /api/admin/settings", s.updateSettings)
	mux.HandleFunc("GET /api/admin/api-keys", s.apiKeys)
	mux.HandleFunc("POST /api/admin/api-keys", s.createAPIKey)
	mux.HandleFunc("DELETE /api/admin/api-keys/{id}", s.deleteAPIKey)
	mux.HandleFunc("GET /api/admin/thinking", s.getGlobalThinking)
	mux.HandleFunc("PUT /api/admin/thinking", s.setGlobalThinking)
	mux.HandleFunc("GET /api/admin/accounts/{id}/thinking", s.getAccountThinking)
	mux.HandleFunc("PUT /api/admin/accounts/{id}/thinking", s.setAccountThinking)
	mux.HandleFunc("DELETE /api/admin/accounts/{id}/thinking", s.deleteAccountThinking)
	mux.HandleFunc("GET /api/admin/models/capabilities", s.modelCapabilities)
	mux.HandleFunc("GET /api/admin/accounts", s.listAccounts)
	mux.HandleFunc("GET /api/admin/accounts/{id}", s.getAccount)
	mux.HandleFunc("POST /api/admin/accounts/{id}/activate", s.activateAccountByPath)
	mux.HandleFunc("GET /api/admin/zcode/environment", s.zcodeEnvironment)
	mux.HandleFunc("POST /api/admin/zcode/accounts/{id}/activate", s.activateAccountInZCode)
	mux.HandleFunc("GET /api/admin/zcode/bridge/status", s.zcodeBridgeStatus)
	mux.HandleFunc("GET /api/admin/zcode/bridge/next", s.zcodeBridgeNext)
	mux.HandleFunc("GET /api/admin/zcode/bridge/ack", s.zcodeBridgeAckQuery)
	mux.HandleFunc("POST /api/admin/zcode/bridge/ack", s.zcodeBridgeAck)
	mux.HandleFunc("PUT /api/admin/accounts/order", s.reorderAccounts)
	mux.HandleFunc("DELETE /api/admin/accounts/{id}", s.deleteAccountByPath)
	mux.HandleFunc("POST /api/admin/auth/login/start", s.loginStart)
	mux.HandleFunc("GET /api/admin/auth/login/poll", s.loginPoll)
	mux.HandleFunc("GET /api/admin/logs", s.systemLogs)
	mux.HandleFunc("GET /api/admin/queue", s.queueSnapshot)
	return s.cors(mux)
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	upstreamConfig := s.loader.Load(nil)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "runtime": "go", "port": s.port, "upstream": upstreamConfig.Endpoint,
		"hasAuthorization": upstreamConfig.HasAuthorization, "source": upstreamConfig.Source, "activeAccount": upstreamConfig.ActiveAccount,
		"models": models.List(), "captchaBridge": s.captcha.Snapshot(), "captchaHeadlessBrowser": s.browser.Snapshot(),
		"settings": s.admin.PublicSnapshot(),
	})
}

func (s *Server) listModels(w http.ResponseWriter, _ *http.Request) {
	data := make([]map[string]any, 0)
	for _, model := range models.List() {
		data = append(data, map[string]any{"id": model.ID, "object": "model", "created": 0, "owned_by": "zcode"})
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (s *Server) modelCapabilities(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"object": "zcode.model_capabilities", "data": models.List()})
}

func (s *Server) chat(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "invalid_request_error")
		return
	}
	model, ok := models.Resolve(stringValue(body["model"]))
	if !ok {
		writeError(w, http.StatusBadRequest, "unsupported model", "invalid_request_error")
		return
	}
	requestID := randomID()
	skipped := map[string]bool{}
	authSkipped := false
	for {
		previousActive := s.accounts.Active()
		selection := s.pool.SelectSkipping(r.Context(), model, skipped)
		if selection.AllExhausted {
			if authSkipped {
				writeError(w, http.StatusUnauthorized, "Todas as contas salvas parecem estar com login expirado. Abra o app, faca login novamente em uma conta Z.ai e tente de novo.", "zcode_all_accounts_auth_failed")
				return
			}
			writeError(w, http.StatusTooManyRequests, "all saved ZCode accounts have exhausted quota for "+model.ID, "zcode_all_accounts_exhausted")
			return
		}
		if !selection.Config.HasAuthorization {
			writeError(w, http.StatusUnauthorized, "Nenhuma conta Z.ai/ZCode esta conectada. Abra o app, adicione uma conta e tente novamente.", "zcode_auth_missing")
			return
		}
		accountID := ""
		if selection.Account != nil {
			accountID = selection.Account.ID
		}
		requiredUnits := s.reserve.Minimum(model.ID, s.cfg.AccountMinAvailable)
		if accountID != "" && selection.Balance != nil && selection.Balance.Available != nil && *selection.Balance.Available < requiredUnits {
			skipped[accountID] = true
			s.logs.add(
				"warn",
				"account.reserve_insufficient",
				fmt.Sprintf(
					"Request %s pulou a conta %s antes do chat: disponivel=%d, minimo dinamico=%d para %s",
					requestID,
					accountID,
					*selection.Balance.Available,
					requiredUnits,
					model.ID,
				),
			)
			fallback := s.pool.SelectSkipping(r.Context(), model, skipped)
			if !fallback.AllExhausted && fallback.Account != nil {
				selection = fallback
				accountID = fallback.Account.ID
			} else {
				delete(skipped, accountID)
			}
		}
		s.logAndSyncAutoRotation(requestID, previousActive, selection.Account, model)
		queueKey := requestqueue.Key(accountID, model.ID)
		thinking := s.admin.ThinkingFor(accountID)
		upstreamBody := openai.ToAnthropic(body, selection.Config.BodyTemplate, model, thinking, s.cfg.DefaultMaxTokens)
		s.logs.add("info", "chat.started", fmt.Sprintf("Request %s iniciado com %s usando conta %s", requestID, model.ID, accountID))
		before, _ := s.quota.ModelBalance(r.Context(), selection.Config, model)
		lease, err := s.queue.Acquire(r.Context(), queueKey)
		if err != nil {
			s.logs.add("warn", "chat.cancelled", fmt.Sprintf("Request %s cancelado enquanto aguardava fila %s: %v", requestID, queueKey, err))
			writeError(w, http.StatusRequestTimeout, "request cancelled while waiting for account/model queue", "zcode_queue_cancelled")
			return
		}
		if lease.Position() > 0 {
			s.logs.add("info", "queue.released", fmt.Sprintf("Request %s liberado apos aguardar fila %s", requestID, queueKey))
			lease.Release()
			continue
		}
		onSuccess := func() {
			s.logs.add("info", "chat.completed", fmt.Sprintf("Request %s concluido com %s", requestID, model.ID))
			if s.cfg.QuotaLog {
				go s.logQuota(requestID, selection.Config, model, before)
			}
		}
		if streaming(body) {
			attempts, err, started := s.streamChat(w, r, selection.Config, upstreamBody, model, onSuccess)
			lease.Release()
			if err == nil {
				return
			}
			if !started && s.skipQuotaExhaustedAccount(requestID, accountID, model, err, skipped) {
				continue
			}
			if !started && s.skipAuthFailedAccount(requestID, accountID, err, skipped) {
				authSkipped = true
				continue
			}
			diagnostic := s.logChatFailure(requestID, attempts, err, body, upstreamBody)
			writeProxyErrorWithDiagnostic(w, err, attempts, diagnostic)
			return
		}
		completion, attempts, err := s.proxy.Collect(r.Context(), selection.Config, upstreamBody)
		lease.Release()
		if err != nil {
			if s.skipQuotaExhaustedAccount(requestID, accountID, model, err, skipped) {
				continue
			}
			if s.skipAuthFailedAccount(requestID, accountID, err, skipped) {
				authSkipped = true
				continue
			}
			diagnostic := s.logChatFailure(requestID, attempts, err, body, upstreamBody)
			writeProxyErrorWithDiagnostic(w, err, attempts, diagnostic)
			return
		}
		message := map[string]any{"role": "assistant", "content": completion.Text}
		if len(completion.ToolCalls) > 0 {
			message["tool_calls"] = completion.ToolCalls
		}
		writeJSON(w, http.StatusOK, map[string]any{"id": "chatcmpl-" + randomID(), "object": "chat.completion", "created": time.Now().Unix(), "model": model.ID, "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": completion.FinishReason}}, "usage": completion.Usage})
		onSuccess()
		return
	}
}

func (s *Server) streamChat(w http.ResponseWriter, r *http.Request, upstreamConfig upstream.Config, body map[string]any, model models.Model, onSuccess func()) (int, error, bool) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming unsupported", "internal_error")
		return 0, nil, true
	}
	started := false
	start := func() {
		if started {
			return
		}
		started = true
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache, no-transform")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
	}
	id := "chatcmpl-" + randomID()
	finalSent := false
	attempts, err := s.proxy.Stream(r.Context(), upstreamConfig, body, func(event proxy.StreamEvent) error {
		start()
		if event.FinishReason != "" {
			if finalSent {
				return nil
			}
			finalSent = true
		}
		chunk := map[string]any{"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model.ID, "choices": []any{map[string]any{"index": 0, "delta": event.Delta, "finish_reason": nullable(event.FinishReason)}}}
		writeSSE(w, chunk)
		flusher.Flush()
		return nil
	})
	if err != nil {
		if !started {
			return attempts, err, false
		}
		writeSSE(w, map[string]any{"error": errorPayload(err)})
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
		return attempts, nil, true
	}
	start()
	if !finalSent {
		writeSSE(w, map[string]any{"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": model.ID, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}})
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	flusher.Flush()
	onSuccess()
	return attempts, nil, true
}

func (s *Server) logAndSyncAutoRotation(requestID string, previousActive *accounts.Account, selected *accounts.Account, model models.Model) {
	if selected == nil {
		return
	}
	if previousActive != nil && previousActive.ID == selected.ID {
		return
	}
	fromLabel := "nenhuma conta anterior"
	if previousActive != nil {
		fromLabel = accounts.Sanitize(*previousActive).Label
	}
	toLabel := accounts.Sanitize(*selected).Label
	s.logs.add(
		"info",
		"account.rotated",
		fmt.Sprintf("Request %s trocou automaticamente a conta de %s para %s ao selecionar %s", requestID, fromLabel, toLabel, model.ID),
	)
	go func(accountID, accountLabel string) {
		result, err := s.applyAccountInZCode(accountID)
		if err != nil {
			s.logs.add("warn", "zcode.auto_apply_failed", "Rotacao automatica selecionou "+accountLabel+", mas nao foi possivel aplicar no ZCode: "+err.Error())
			return
		}
		if result != nil {
			s.logs.add("info", "zcode.auto_applied", "Rotacao automatica aplicou "+accountLabel+" no ambiente interno do ZCode")
		}
	}(selected.ID, toLabel)
}

func (s *Server) skipQuotaExhaustedAccount(requestID, accountID string, model models.Model, err error, skipped map[string]bool) bool {
	if accountID == "" || skipped[accountID] || !proxy.IsQuotaExhausted(err) {
		return false
	}
	skipped[accountID] = true
	s.logs.add("warn", "account.quota_exhausted", fmt.Sprintf("Request %s detectou cota esgotada para %s na conta %s; tentando proxima conta", requestID, model.ID, accountID))
	return true
}

func (s *Server) skipAuthFailedAccount(requestID, accountID string, err error, skipped map[string]bool) bool {
	if accountID == "" || skipped[accountID] || !proxy.IsAuthFailed(err) {
		return false
	}
	skipped[accountID] = true
	s.logs.add("warn", "account.auth_failed", fmt.Sprintf("Request %s recebeu erro de login na conta %s; tentando proxima conta salva", requestID, accountID))
	return true
}

func (s *Server) logChatFailure(requestID string, attempts int, err error, clientBody, upstreamBody map[string]any) map[string]any {
	if captchaErr, ok := captcha.Classify(err); ok {
		event, message := s.captchaLogMessage(requestID, attempts, captchaErr)
		s.logs.add("warn", event, message)
		return nil
	}
	if proxy.IsParameterError(err) {
		diagnostic := sanitizedPayloadDiagnostic(clientBody, upstreamBody)
		raw, _ := json.Marshal(diagnostic)
		s.logs.add("error", "upstream.parameter_error", fmt.Sprintf("Request %s falhou: a Z.ai recusou algum parametro do payload traduzido. Diagnostico sanitizado: %s", requestID, string(raw)))
		return diagnostic
	}
	s.logs.add("error", "chat.failed", fmt.Sprintf("Request %s falhou apos %d tentativa(s): %v", requestID, attempts, err))
	return nil
}

func (s *Server) captchaLogMessage(requestID string, attempts int, err *captcha.Error) (string, string) {
	captchaURL := fmt.Sprintf("http://127.0.0.1:%d/zcode/captcha/browser?client=standalone-browser", s.port)
	switch err.Kind {
	case captcha.ErrDisabled:
		return "captcha.disabled", fmt.Sprintf("Request %s falhou: a Z.ai pediu captcha, mas o captcha bridge esta desativado. Ative o captcha bridge ou configure ZCODE_CAPTCHA_BRIDGE=true.", requestID)
	case captcha.ErrBrowserUnavailable:
		snapshot := s.browser.Snapshot()
		lastError := browserLastError(snapshot)
		if strings.Contains(strings.ToLower(lastError), "no supported chrome or edge") {
			return "captcha.browser_missing", fmt.Sprintf("Request %s falhou: a Z.ai pediu captcha, mas nao encontrei Chrome nem Edge instalado para abrir o navegador captcha automatico. Instale Chrome/Edge ou abra manualmente %s.", requestID, captchaURL)
		}
		if !snapshot.Enabled {
			return "captcha.browser_disabled", fmt.Sprintf("Request %s falhou: a Z.ai pediu captcha, mas o navegador captcha automatico esta desativado. Abra manualmente %s.", requestID, captchaURL)
		}
		if lastError != "" {
			return "captcha.browser_unavailable", fmt.Sprintf("Request %s falhou: a Z.ai pediu captcha, mas o navegador captcha nao ficou disponivel. Ultimo erro do browser: %s. Abra manualmente %s.", requestID, lastError, captchaURL)
		}
		return "captcha.browser_unavailable", fmt.Sprintf("Request %s falhou: a Z.ai pediu captcha, mas nenhum navegador captcha esta conectado ao proxy. Abra %s e tente novamente.", requestID, captchaURL)
	case captcha.ErrInteractiveRequired:
		return "captcha.interactive_required", fmt.Sprintf("Request %s falhou: a Z.ai pediu captcha interativo. Abra %s, resolva a verificacao e tente novamente.", requestID, captchaURL)
	case captcha.ErrTimeout:
		return "captcha.timeout", fmt.Sprintf("Request %s falhou apos %d tentativa(s): a Z.ai pediu captcha, mas o navegador captcha nao respondeu dentro do tempo limite. Abra %s e tente novamente.", requestID, attempts, captchaURL)
	case captcha.ErrEmptyToken:
		return "captcha.empty_token", fmt.Sprintf("Request %s falhou: o navegador captcha respondeu sem token valido. Abra %s, resolva a verificacao e tente novamente.", requestID, captchaURL)
	default:
		return "captcha.failed", fmt.Sprintf("Request %s falhou por captcha: %s. Abra %s e tente novamente.", requestID, err.Message, captchaURL)
	}
}

func browserLastError(snapshot captcha.BrowserSnapshot) string {
	if snapshot.LastExit == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(snapshot.LastExit["error"]))
}

func sanitizedPayloadDiagnostic(clientBody, upstreamBody map[string]any) map[string]any {
	return map[string]any{
		"client_request":   sanitizeDiagnosticValue("", clientBody, 0),
		"translated_body":  sanitizeDiagnosticValue("", upstreamBody, 0),
		"sanitization":     "text fields are summarized by length; credentials/captcha values are redacted",
		"probable_problem": "check unsupported top-level params, message content shape, thinking/max_tokens, tools and tool_choice",
	}
}

func sanitizeDiagnosticValue(key string, value any, depth int) any {
	if depth > 6 {
		return "<max_depth>"
	}
	key = strings.ToLower(key)
	if sensitiveDiagnosticKey(key) {
		if strings.TrimSpace(fmt.Sprint(value)) == "" {
			return ""
		}
		return "<redacted>"
	}
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for childKey, childValue := range typed {
			out[childKey] = sanitizeDiagnosticValue(childKey, childValue, depth+1)
		}
		return out
	case []any:
		limit := len(typed)
		if limit > 12 {
			limit = 12
		}
		out := make([]any, 0, limit+1)
		for index := 0; index < limit; index++ {
			out = append(out, sanitizeDiagnosticValue(key, typed[index], depth+1))
		}
		if len(typed) > limit {
			out = append(out, map[string]any{"omitted_items": len(typed) - limit})
		}
		return out
	case string:
		if textDiagnosticKey(key) {
			return map[string]any{"type": "string", "length": len(typed)}
		}
		return truncateString(typed, 180)
	default:
		return typed
	}
}

func sensitiveDiagnosticKey(key string) bool {
	for _, marker := range []string{"authorization", "token", "secret", "password", "api_key", "apikey", "captcha", "cookie"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}

func textDiagnosticKey(key string) bool {
	switch key {
	case "content", "text", "system", "thinking", "partial_json", "arguments":
		return true
	default:
		return false
	}
}

func truncateString(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	if limit <= 12 {
		return value[:limit]
	}
	return value[:limit-12] + "...<truncated>"
}

func (s *Server) listAccounts(w http.ResponseWriter, r *http.Request) {
	activeID, publicAccounts := s.accounts.Public()
	includeQuota := accountListIncludesQuota(r)
	if !includeQuota {
		data := make([]map[string]any, 0, len(publicAccounts))
		for _, public := range publicAccounts {
			value := mapFrom(public)
			value["credentialSource"] = "zcode-oauth-cli"
			value["quota"] = nil
			value["quotaError"] = nil
			value["quotaSkipped"] = true
			data = append(data, value)
		}
		writeJSON(w, http.StatusOK, map[string]any{"object": "list", "activeAccountId": activeID, "refreshSupported": false, "loginRequiredOnExpiry": true, "data": data})
		return
	}

	type result struct {
		index int
		value map[string]any
	}
	data := make([]map[string]any, len(publicAccounts))
	for index, public := range publicAccounts {
		account := s.accounts.Get(public.ID)
		value := mapFrom(public)
		value["credentialSource"] = "zcode-oauth-cli"
		if account == nil {
			value["quota"] = nil
			value["quotaError"] = map[string]any{"message": "account not found", "type": "not_found"}
			data[index] = value
			continue
		}
		quotaCtx, cancel := context.WithTimeout(r.Context(), accountListQuotaTimeout)
		snapshot, err := s.quota.BalanceSnapshot(quotaCtx, s.loader.Load(account))
		cancel()
		if err != nil {
			value["quota"] = nil
			value["quotaError"] = map[string]any{"message": err.Error(), "type": "zcode_quota_fetch_failed"}
		} else {
			value["quota"] = snapshot
			value["quotaError"] = nil
		}
		data[index] = value
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "activeAccountId": activeID, "refreshSupported": false, "loginRequiredOnExpiry": true, "data": data})
}

func accountListIncludesQuota(r *http.Request) bool {
	value := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("quota")))
	if value == "" {
		value = strings.ToLower(strings.TrimSpace(r.URL.Query().Get("include_quota")))
	}
	switch value {
	case "0", "false", "no", "off", "skip", "none":
		return false
	default:
		return true
	}
}

func (s *Server) getAccount(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	account := s.accounts.Get(id)
	if account == nil {
		writeError(w, http.StatusNotFound, "account not found", "not_found")
		return
	}
	public := accounts.Sanitize(*account)
	active := s.accounts.Active()
	public.Active = active != nil && active.ID == id
	_, queued := s.accounts.Public()
	for _, item := range queued {
		if item.ID == id {
			public.QueuePosition = item.QueuePosition
		}
	}
	value := mapFrom(public)
	value["object"] = "zcode.account"
	value["credentialSource"] = "zcode-oauth-cli"
	quotaCtx, cancel := context.WithTimeout(r.Context(), accountListQuotaTimeout)
	defer cancel()
	snapshot, err := s.quota.BalanceSnapshot(quotaCtx, s.loader.Load(account))
	if err != nil {
		value["quota"] = nil
		value["quotaError"] = map[string]any{"message": err.Error(), "type": "zcode_quota_fetch_failed"}
	} else {
		value["quota"] = snapshot
		value["quotaError"] = nil
	}
	writeJSON(w, http.StatusOK, value)
}

func (s *Server) activeQuota(w http.ResponseWriter, r *http.Request) {
	snapshot, err := s.quota.Snapshot(r.Context(), s.loader.Load(nil))
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), "zcode_quota_fetch_failed")
		return
	}
	writeJSON(w, http.StatusOK, snapshot)
}

func (s *Server) accountPool(w http.ResponseWriter, r *http.Request) {
	model, ok := models.Resolve(r.URL.Query().Get("model"))
	if !ok {
		writeError(w, http.StatusBadRequest, "unsupported model", "invalid_request_error")
		return
	}
	writeJSON(w, http.StatusOK, s.pool.Snapshot(r.Context(), model))
}

func (s *Server) authStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"activeAccount": sanitizePointer(s.accounts.Active()), "pendingFlows": s.oauth.Status()})
}

func (s *Server) authAccounts(w http.ResponseWriter, _ *http.Request) {
	activeID, items := s.accounts.Public()
	writeJSON(w, http.StatusOK, map[string]any{"activeAccountId": activeID, "accounts": items, "refreshSupported": false, "loginRequiredOnExpiry": true})
}

func (s *Server) loginStart(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	flow, err := s.oauth.Start(r.Context())
	elapsed := time.Since(started)
	if err != nil {
		s.logs.add("warn", "auth.start_failed", fmt.Sprintf("Login ZCode falhou apos %s ao iniciar o fluxo OAuth: %s", elapsed, err))
		writeError(w, http.StatusBadGateway, err.Error(), "zcode_auth_flow_failed")
		return
	}
	s.logs.add("info", "auth.started", fmt.Sprintf("Novo login ZCode iniciado em %s (flow %s)", elapsed, flow.FlowID))
	writeJSON(w, http.StatusCreated, flow)
}

func (s *Server) loginPoll(w http.ResponseWriter, r *http.Request) {
	flowID := r.URL.Query().Get("flow_id")
	if flowID == "" {
		writeError(w, http.StatusBadRequest, "flow_id is required", "invalid_request_error")
		return
	}
	started := time.Now()
	result, err := s.oauth.Poll(r.Context(), flowID)
	elapsed := time.Since(started)
	if err != nil {
		s.logs.add("warn", "auth.poll_failed", fmt.Sprintf("Poll do flow %s falhou apos %s: %s", flowID, elapsed, err))
		writeError(w, http.StatusBadGateway, err.Error(), "zcode_auth_flow_failed")
		return
	}
	if result["status"] == "ready" {
		s.logs.add("info", "auth.completed", fmt.Sprintf("Conta ZCode autenticada e adicionada à fila (flow %s concluido apos %s)", flowID, elapsed))
	} else if elapsed > time.Second {
		s.logs.add("warn", "auth.poll_slow", fmt.Sprintf("Poll do flow %s levou %s para retornar status=%v", flowID, elapsed, result["status"]))
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) activateAccount(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AccountID string `json:"account_id"`
	}
	if decodeJSON(w, r, &body) != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON", "invalid_request_error")
		return
	}
	s.activate(w, body.AccountID)
}

func (s *Server) activateAccountByPath(w http.ResponseWriter, r *http.Request) {
	s.activate(w, r.PathValue("id"))
}

func (s *Server) activate(w http.ResponseWriter, id string) {
	account, err := s.accounts.Activate(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "storage_error")
		return
	}
	if account == nil {
		writeError(w, http.StatusNotFound, "account not found", "not_found")
		return
	}
	s.logs.add("info", "account.activated", "Conta ativa alterada para "+account.Label)
	zcodeResult, zcodeErr := s.applyAccountInZCode(account.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"activeAccount": account,
		"zcode": map[string]any{
			"synced": zcodeErr == nil && zcodeResult != nil,
			"error":  nullableError(zcodeErr),
			"result": zcodeResult,
		},
	})
}

func (s *Server) reorderAccounts(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AccountIDs []string `json:"accountIds"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "invalid_request_error")
		return
	}
	if err := s.accounts.Reorder(body.AccountIDs); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "invalid_account_order")
		return
	}
	s.logs.add("info", "accounts.reordered", "Ordem da fila de contas atualizada")
	activeID, items := s.accounts.Public()
	writeJSON(w, http.StatusOK, map[string]any{"activeAccountId": activeID, "data": items})
}

func (s *Server) deleteAccount(w http.ResponseWriter, r *http.Request) {
	s.removeAccount(w, r.URL.Query().Get("account_id"))
}

func (s *Server) deleteAccountByPath(w http.ResponseWriter, r *http.Request) {
	s.removeAccount(w, r.PathValue("id"))
}

func (s *Server) removeAccount(w http.ResponseWriter, id string) {
	previousActive := s.accounts.Active()
	removedActive := previousActive != nil && previousActive.ID == id
	removed, err := s.accounts.Remove(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "storage_error")
		return
	}
	if !removed {
		writeError(w, http.StatusNotFound, "account not found", "not_found")
		return
	}
	s.logs.add("warn", "account.removed", "Conta removida do pool")
	_ = s.admin.SetAccountThinking(id, nil)

	var zcodeResult any
	var zcodeErr error
	nextActive := s.accounts.Active()
	if removedActive && nextActive != nil {
		zcodeResult, zcodeErr = s.applyAccountInZCode(nextActive.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"removed":       true,
		"accountId":     id,
		"activeAccount": sanitizePointer(nextActive),
		"zcode": map[string]any{
			"synced": zcodeErr == nil && zcodeResult != nil,
			"error":  nullableError(zcodeErr),
			"result": zcodeResult,
		},
	})
}

func (s *Server) captchaConfig(w http.ResponseWriter, r *http.Request) {
	value, err := captcha.FetchConfig(r.Context(), s.cfg)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error(), "zcode_captcha_config_failed")
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (s *Server) captchaBrowser(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprint(w, captcha.BrowserPage)
}

func (s *Server) adminOverview(w http.ResponseWriter, r *http.Request) {
	activeID, items := s.accounts.Public()
	writeJSON(w, http.StatusOK, map[string]any{"runtime": "go", "port": s.port, "activeAccountId": activeID, "accountCount": len(items), "models": models.List(), "settings": s.admin.PublicSnapshot(), "captcha": s.captcha.Snapshot(), "browser": s.browser.Snapshot()})
}

func (s *Server) adminSettings(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.admin.PublicSnapshot())
}

func (s *Server) updateSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Port       *int  `json:"port"`
		APIEnabled *bool `json:"apiEnabled"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "invalid_request_error")
		return
	}
	if body.Port != nil {
		if err := s.admin.SetPort(*body.Port); err != nil {
			writeError(w, http.StatusBadRequest, err.Error(), "invalid_request_error")
			return
		}
	}
	if body.APIEnabled != nil {
		if err := s.admin.SetAPIEnabled(*body.APIEnabled); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error(), "storage_error")
			return
		}
		state := "parada"
		if *body.APIEnabled {
			state = "iniciada"
		}
		s.logs.add("info", "api.state_changed", "API OpenAI "+state+" pelo painel")
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": s.admin.PublicSnapshot(), "restartRequired": body.Port != nil && *body.Port != s.port})
}

func (s *Server) apiKeys(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": s.admin.PublicSnapshot()["apiKeys"]})
}

func (s *Server) createAPIKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	_ = decodeJSON(w, r, &body)
	key, secret, err := s.admin.CreateAPIKey(body.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error(), "storage_error")
		return
	}
	s.logs.add("info", "api_key.created", "Nova API key criada: "+state.PublicKey(key).Name)
	writeJSON(w, http.StatusCreated, map[string]any{"apiKey": state.PublicKey(key), "secret": secret, "warning": "The secret is returned only once."})
}

func (s *Server) deleteAPIKey(w http.ResponseWriter, r *http.Request) {
	if !s.admin.DeleteAPIKey(r.PathValue("id")) {
		writeError(w, http.StatusNotFound, "API key not found", "not_found")
		return
	}
	s.logs.add("warn", "api_key.deleted", "API key removida")
	writeJSON(w, http.StatusOK, map[string]any{"removed": true})
}

func (s *Server) getGlobalThinking(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.admin.Snapshot().GlobalThinking)
}

func (s *Server) setGlobalThinking(w http.ResponseWriter, r *http.Request) {
	var value state.ThinkingSettings
	if err := decodeJSON(w, r, &value); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "invalid_request_error")
		return
	}
	if err := s.admin.SetGlobalThinking(value); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "invalid_request_error")
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func (s *Server) getAccountThinking(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	settings := s.admin.Snapshot()
	override, exists := settings.AccountThinking[id]
	writeJSON(w, http.StatusOK, map[string]any{"accountId": id, "override": nullableThinking(override, exists), "effective": s.admin.ThinkingFor(id), "inherited": !exists})
}

func (s *Server) setAccountThinking(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.accounts.Get(id) == nil {
		writeError(w, http.StatusNotFound, "account not found", "not_found")
		return
	}
	var value state.ThinkingSettings
	if err := decodeJSON(w, r, &value); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "invalid_request_error")
		return
	}
	if err := s.admin.SetAccountThinking(id, &value); err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "invalid_request_error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accountId": id, "override": value, "effective": value, "inherited": false})
}

func (s *Server) deleteAccountThinking(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	_ = s.admin.SetAccountThinking(id, nil)
	writeJSON(w, http.StatusOK, map[string]any{"accountId": id, "override": nil, "effective": s.admin.ThinkingFor(id), "inherited": true})
}

func (s *Server) requireAPIKey(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.admin.Snapshot().APIEnabled {
			writeError(w, http.StatusServiceUnavailable, "OpenAI-compatible API is stopped", "api_stopped")
			return
		}
		if !s.admin.ValidateAPIKey(r.Header.Get("Authorization")) {
			writeError(w, http.StatusUnauthorized, "invalid API key", "invalid_api_key")
			return
		}
		next(w, r)
	}
}

func (s *Server) systemLogs(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 200
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": s.logs.list(limit)})
}

func (s *Server) queueSnapshot(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"object": "zcode.request_queue", "data": s.queue.Snapshot()})
}

func (s *Server) logQuota(requestID string, upstreamConfig upstream.Config, model models.Model, before *quota.Balance) {
	var after *quota.Balance
	for attempt := 0; attempt < s.cfg.QuotaRefreshAttempts; attempt++ {
		time.Sleep(s.cfg.QuotaRefreshDelay)
		value, err := s.quota.ModelBalance(context.Background(), upstreamConfig, model)
		if err == nil {
			after = value
		}
		if changed(before, after) {
			break
		}
	}
	if usedDelta, ok := deltaInt(before, after); ok {
		s.reserve.Observe(model.ID, usedDelta)
	}
	log.Printf("[quota] request=%s model=%s antiga used=%s remaining=%s available=%s -> atualizada used=%s remaining=%s available=%s deltaUsed=%s", requestID, model.UpstreamID, pointer(before, func(v *quota.Balance) *int64 { return v.Used }), pointer(before, func(v *quota.Balance) *int64 { return v.Remaining }), pointer(before, func(v *quota.Balance) *int64 { return v.Available }), pointer(after, func(v *quota.Balance) *int64 { return v.Used }), pointer(after, func(v *quota.Balance) *int64 { return v.Remaining }), pointer(after, func(v *quota.Balance) *int64 { return v.Available }), delta(before, after))
	s.logs.add("info", "quota.updated", fmt.Sprintf(
		"Request %s · %s · cota antiga %s usados/%s disponíveis → cota nova %s usados/%s disponíveis · delta %s",
		requestID,
		model.UpstreamID,
		pointer(before, func(v *quota.Balance) *int64 { return v.Used }),
		pointer(before, func(v *quota.Balance) *int64 { return v.Available }),
		pointer(after, func(v *quota.Balance) *int64 { return v.Used }),
		pointer(after, func(v *quota.Balance) *int64 { return v.Available }),
		delta(before, after),
	))
}

func (s *Server) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type,Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, 20<<20)).Decode(target)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message, kind string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"message": message, "type": kind}})
}

func writeProxyError(w http.ResponseWriter, err error, attempts int) {
	writeProxyErrorWithDiagnostic(w, err, attempts, nil)
}

func writeProxyErrorWithDiagnostic(w http.ResponseWriter, err error, attempts int, diagnostic map[string]any) {
	status := http.StatusBadGateway
	if value, ok := err.(*proxy.UpstreamError); ok && value.Status >= 400 {
		status = value.Status
	}
	payload := errorPayload(err)
	payload["attempts"] = attempts
	if diagnostic != nil {
		payload["request_diagnostic"] = diagnostic
	}
	writeJSON(w, status, map[string]any{"error": payload})
}

func errorPayload(err error) map[string]any {
	if captchaErr, ok := captcha.Classify(err); ok {
		message := friendlyCaptchaErrorMessage(captchaErr)
		payload := map[string]any{"message": message, "type": "zcode_" + captchaErr.Kind}
		if captchaErr.Message != "" && captchaErr.Message != message {
			payload["technical_message"] = captchaErr.Message
		}
		return payload
	}
	if value, ok := err.(*proxy.UpstreamError); ok {
		message := friendlyErrorMessage(value)
		payload := map[string]any{"message": message, "type": value.Type, "code": value.Code, "request_id": value.RequestID, "status": value.Status}
		if message != value.Message {
			payload["technical_message"] = value.Message
		}
		return payload
	}
	return map[string]any{"message": err.Error(), "type": "upstream_error"}
}

func friendlyCaptchaErrorMessage(err *captcha.Error) string {
	switch err.Kind {
	case captcha.ErrDisabled:
		return "A Z.ai pediu captcha, mas o captcha bridge esta desativado no proxy."
	case captcha.ErrBrowserUnavailable:
		return "A Z.ai pediu captcha, mas nenhum navegador captcha esta disponivel. Abra /zcode/captcha/browser e tente novamente."
	case captcha.ErrInteractiveRequired:
		return "A Z.ai pediu captcha interativo. Abra /zcode/captcha/browser, resolva a verificacao e tente novamente."
	case captcha.ErrTimeout:
		return "A Z.ai pediu captcha, mas o navegador captcha demorou demais para responder. Abra /zcode/captcha/browser e tente novamente."
	case captcha.ErrEmptyToken:
		return "A Z.ai pediu captcha, mas o navegador retornou uma verificacao vazia. Abra /zcode/captcha/browser e tente novamente."
	default:
		return "A Z.ai pediu captcha. Abra /zcode/captcha/browser, resolva a verificacao e tente novamente."
	}
}

func friendlyErrorMessage(err *proxy.UpstreamError) string {
	switch {
	case proxy.IsAdmissionConcurrency(err):
		return "Opa, os servidores da Z.ai estao cheios no momento. Tente novamente em instantes."
	case proxy.IsAuthFailed(err):
		return "A sessao da Z.ai expirou ou ficou invalida. Abra o app, faca login novamente nessa conta e tente de novo."
	case proxy.IsQuotaExhausted(err):
		return "A cota dessa conta acabou para este modelo. O proxy vai tentar outra conta salva quando houver uma disponivel."
	case err.Type == "stale_connection":
		return "A conexao com a Z.ai caiu antes de gerar uma resposta completa. Tente novamente."
	}
	value := strings.ToLower(strings.Join([]string{err.Message, err.Type, fmt.Sprint(err.Code)}, " "))
	if strings.Contains(value, "overloaded") || strings.Contains(value, "temporarily unavailable") {
		return "Opa, os servidores da Z.ai estao instaveis no momento. Tente novamente em instantes."
	}
	return err.Message
}

func writeSSE(w http.ResponseWriter, value any) {
	raw, _ := json.Marshal(value)
	fmt.Fprintf(w, "data: %s\n\n", raw)
}

func streaming(body map[string]any) bool {
	value, ok := body["stream"].(bool)
	return !ok || value
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func nullableError(err error) any {
	if err == nil {
		return nil
	}
	return err.Error()
}

func nullableThinking(value state.ThinkingSettings, exists bool) any {
	if !exists {
		return nil
	}
	return value
}

func sanitizePointer(account *accounts.Account) any {
	if account == nil {
		return nil
	}
	return accounts.Sanitize(*account)
}

func mapFrom(value any) map[string]any {
	raw, _ := json.Marshal(value)
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	return result
}

func stringValue(value any) string {
	result, _ := value.(string)
	return result
}

func randomID() string {
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

func changed(before, after *quota.Balance) bool {
	if after == nil {
		return false
	}
	if before == nil {
		return true
	}
	return fmt.Sprint(before.Used, before.Remaining, before.Available) != fmt.Sprint(after.Used, after.Remaining, after.Available)
}

func pointer(value *quota.Balance, field func(*quota.Balance) *int64) string {
	if value == nil || field(value) == nil {
		return "unknown"
	}
	return strconv.FormatInt(*field(value), 10)
}

func delta(before, after *quota.Balance) string {
	if before == nil || after == nil || before.Used == nil || after.Used == nil {
		return "unknown"
	}
	return strconv.FormatInt(*after.Used-*before.Used, 10)
}

func deltaInt(before, after *quota.Balance) (int64, bool) {
	if before == nil || after == nil || before.Used == nil || after.Used == nil {
		return 0, false
	}
	value := *after.Used - *before.Used
	if value <= 0 {
		return 0, false
	}
	return value, true
}

func (s *Server) Port() int { return s.port }
