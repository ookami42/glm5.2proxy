package tests

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"glm5.2proxy/internal/accountpool"
	"glm5.2proxy/internal/accounts"
	"glm5.2proxy/internal/api"
	"glm5.2proxy/internal/auth"
	"glm5.2proxy/internal/captcha"
	"glm5.2proxy/internal/proxy"
	"glm5.2proxy/internal/quota"
	"glm5.2proxy/internal/state"
	"glm5.2proxy/internal/upstream"
)

func TestAdministrativeAPIAndAPIKeyProtection(t *testing.T) {
	cfg := testConfig(t)
	admin, _ := state.NewAdminStore(cfg.AdminPath, 3005, state.ThinkingSettings{Enabled: true, BudgetTokens: 32000, Effort: "max"})
	accountStore, _ := accounts.NewStore(cfg.CredentialsPath, cfg.CredentialSecret)
	loader := upstream.NewLoader(cfg, accountStore)
	quotaService := quota.New(cfg)
	bridge := captcha.NewBridge(cfg)
	browser := captcha.NewBrowserManager(cfg, 3005)
	server := api.New(cfg, 3005, admin, accountStore, auth.New(cfg, accountStore), quotaService, accountpool.New(cfg, accountStore, loader, quotaService), loader, bridge, browser, proxy.New(cfg, bridge))
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	response, err := http.Get(httpServer.URL + "/api/admin/models/capabilities")
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("capabilities endpoint failed: %v status=%v", err, response.StatusCode)
	}
	var capabilities map[string]any
	json.NewDecoder(response.Body).Decode(&capabilities)
	response.Body.Close()
	if len(capabilities["data"].([]any)) != 2 {
		t.Fatalf("unexpected capabilities: %+v", capabilities)
	}

	create, _ := http.Post(httpServer.URL+"/api/admin/api-keys", "application/json", strings.NewReader(`{"name":"IDE"}`))
	var created map[string]any
	json.NewDecoder(create.Body).Decode(&created)
	create.Body.Close()
	secret, _ := created["secret"].(string)
	if secret == "" {
		t.Fatalf("API key secret missing: %+v", created)
	}
	unauthorized, _ := http.Get(httpServer.URL + "/v1/models")
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected API key enforcement, got %d", unauthorized.StatusCode)
	}
	unauthorized.Body.Close()
	request, _ := http.NewRequest(http.MethodGet, httpServer.URL+"/v1/models", nil)
	request.Header.Set("Authorization", "Bearer "+secret)
	authorized, _ := http.DefaultClient.Do(request)
	if authorized.StatusCode != http.StatusOK {
		t.Fatalf("valid API key rejected: %d", authorized.StatusCode)
	}
	authorized.Body.Close()
}

func TestChatRequestsAreQueuedPerDefaultAccountAndModel(t *testing.T) {
	var active int32
	var maxActive int32
	fakeUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/messages":
			current := atomic.AddInt32(&active, 1)
			for {
				previous := atomic.LoadInt32(&maxActive)
				if current <= previous || atomic.CompareAndSwapInt32(&maxActive, previous, current) {
					break
				}
			}
			time.Sleep(50 * time.Millisecond)
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n"))
			_, _ = w.Write([]byte("data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n"))
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
			atomic.AddInt32(&active, -1)
		case r.URL.Path == "/billing/balance":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"code":0,"data":{"balances":[{"show_name":"GLM-5.2","total_units":3000000,"used_units":0,"remaining_units":3000000,"available_units":3000000}]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer fakeUpstream.Close()

	cfg := testConfig(t)
	cfg.Authorization = "Bearer test-token"
	cfg.UpstreamURL = fakeUpstream.URL + "/messages"
	cfg.BillingBaseURL = fakeUpstream.URL + "/billing"
	admin, _ := state.NewAdminStore(cfg.AdminPath, 3005, state.ThinkingSettings{Enabled: true, BudgetTokens: 32000, Effort: "max"})
	accountStore, _ := accounts.NewStore(cfg.CredentialsPath, cfg.CredentialSecret)
	loader := upstream.NewLoader(cfg, accountStore)
	quotaService := quota.New(cfg)
	bridge := captcha.NewBridge(cfg)
	browser := captcha.NewBrowserManager(cfg, 3005)
	server := api.New(cfg, 3005, admin, accountStore, auth.New(cfg, accountStore), quotaService, accountpool.New(cfg, accountStore, loader, quotaService), loader, bridge, browser, proxy.New(cfg, bridge))
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	body := []byte(`{"model":"glm-5.2","messages":[{"role":"user","content":"oi"}],"stream":false}`)
	var wait sync.WaitGroup
	statuses := make(chan int, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			response, err := http.Post(httpServer.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
			if err != nil {
				t.Errorf("chat request failed: %v", err)
				return
			}
			defer response.Body.Close()
			statuses <- response.StatusCode
		}()
	}
	wait.Wait()
	close(statuses)
	for status := range statuses {
		if status != http.StatusOK {
			t.Fatalf("expected chat status 200, got %d", status)
		}
	}
	if maxActive != 1 {
		t.Fatalf("expected upstream serialization, got max active %d", maxActive)
	}
}

func TestAPIStateReorderAndLogs(t *testing.T) {
	cfg := testConfig(t)
	admin, _ := state.NewAdminStore(cfg.AdminPath, 3005, state.ThinkingSettings{Enabled: true, BudgetTokens: 32000, Effort: "max"})
	accountStore, _ := accounts.NewStore(cfg.CredentialsPath, cfg.CredentialSecret)
	_, _ = accountStore.Upsert(accounts.User{UserID: "one"}, "token-one", "")
	_, _ = accountStore.Upsert(accounts.User{UserID: "two"}, "token-two", "")
	loader := upstream.NewLoader(cfg, accountStore)
	quotaService := quota.New(cfg)
	bridge := captcha.NewBridge(cfg)
	browser := captcha.NewBrowserManager(cfg, 3005)
	server := api.New(cfg, 3005, admin, accountStore, auth.New(cfg, accountStore), quotaService, accountpool.New(cfg, accountStore, loader, quotaService), loader, bridge, browser, proxy.New(cfg, bridge))
	httpServer := httptest.NewServer(server.Handler())
	defer httpServer.Close()

	reorder, _ := http.NewRequest(http.MethodPut, httpServer.URL+"/api/admin/accounts/order", strings.NewReader(`{"accountIds":["two","one"]}`))
	reorder.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(reorder)
	if err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("reorder failed: %v status=%v", err, response.StatusCode)
	}
	response.Body.Close()
	if accountStore.Accounts()[0].ID != "two" {
		t.Fatalf("account order was not updated: %+v", accountStore.Accounts())
	}

	stop, _ := http.NewRequest(http.MethodPatch, httpServer.URL+"/api/admin/settings", strings.NewReader(`{"apiEnabled":false}`))
	stop.Header.Set("Content-Type", "application/json")
	response, _ = http.DefaultClient.Do(stop)
	response.Body.Close()
	modelsResponse, _ := http.Get(httpServer.URL + "/v1/models")
	if modelsResponse.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected stopped API to return 503, got %d", modelsResponse.StatusCode)
	}
	modelsResponse.Body.Close()

	start, _ := http.NewRequest(http.MethodPatch, httpServer.URL+"/api/admin/settings", strings.NewReader(`{"apiEnabled":true}`))
	start.Header.Set("Content-Type", "application/json")
	response, _ = http.DefaultClient.Do(start)
	response.Body.Close()
	modelsResponse, _ = http.Get(httpServer.URL + "/v1/models")
	if modelsResponse.StatusCode != http.StatusOK {
		t.Fatalf("expected restarted API to return 200, got %d", modelsResponse.StatusCode)
	}
	modelsResponse.Body.Close()

	logsResponse, _ := http.Get(httpServer.URL + "/api/admin/logs")
	var logs map[string]any
	_ = json.NewDecoder(logsResponse.Body).Decode(&logs)
	logsResponse.Body.Close()
	if len(logs["data"].([]any)) < 3 {
		t.Fatalf("expected real administrative logs, got %+v", logs)
	}
}
