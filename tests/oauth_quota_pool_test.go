package tests

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"glm5.2proxy/internal/accountpool"
	"glm5.2proxy/internal/accounts"
	"glm5.2proxy/internal/auth"
	"glm5.2proxy/internal/models"
	"glm5.2proxy/internal/quota"
	"glm5.2proxy/internal/upstream"
)

func TestOAuthQuotaAndAccountRotation(t *testing.T) {
	var chatToken string
	var initAuth string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/oauth/cli/init":
			initAuth = r.Header.Get("Authorization")
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"flow_id": "flow-1", "poll_token": "server-returned-token", "authorize_url": "https://example.test/login", "expires_at": 4102444800, "poll_interval_sec": 1}})
		case r.URL.Path == "/oauth/cli/poll/flow-1":
			if r.Header.Get("Authorization") != initAuth {
				http.Error(w, `{"code":3001,"msg":"wrong poll token"}`, http.StatusUnauthorized)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"status": "ready", "token": "token-one", "user": map[string]any{"user_id": "one", "email": "one@example.test"}, "zai": map[string]any{"access_token": "access-one"}}})
		case r.URL.Path == "/billing/current":
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"plans": []any{}}})
		case r.URL.Path == "/billing/balance":
			chatToken = r.Header.Get("Authorization")
			available := 0
			if chatToken == "Bearer token-two" {
				available = 500
			}
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"balances": []any{map[string]any{"show_name": "GLM-5.2", "total_units": 1000, "used_units": 1000 - available, "remaining_units": available, "available_units": available}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer mock.Close()

	cfg := testConfig(t)
	cfg.OAuthBaseURL = mock.URL
	cfg.BillingBaseURL = mock.URL + "/billing"
	store, _ := accounts.NewStore(cfg.CredentialsPath, cfg.CredentialSecret)
	oauth := auth.New(cfg, store)
	flow, err := oauth.Start(context.Background())
	if err != nil || flow.FlowID != "flow-1" {
		t.Fatalf("OAuth start failed: %+v %v", flow, err)
	}
	result, err := oauth.Poll(context.Background(), flow.FlowID)
	if err != nil || result["status"] != "ready" {
		t.Fatalf("OAuth poll failed: %+v %v", result, err)
	}
	if _, err := store.Upsert(accounts.User{UserID: "two"}, "token-two", ""); err != nil {
		t.Fatal(err)
	}
	loader := upstream.NewLoader(cfg, store)
	quotaService := quota.New(cfg)
	pool := accountpool.New(cfg, store, loader, quotaService)
	model, _ := models.Resolve("glm-5.2")
	selection := pool.Select(context.Background(), model)
	if selection.Account == nil || selection.Account.ID != "two" || !selection.Rotated {
		t.Fatalf("account did not rotate: %+v", selection)
	}
	if store.Active().ID != "two" || chatToken != "Bearer token-two" {
		t.Fatalf("wrong active account/token: active=%+v token=%s", store.Active(), chatToken)
	}
}

func TestOAuthStartReportsEmptyHTTPStatusInsteadOfEOF(t *testing.T) {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer mock.Close()

	cfg := testConfig(t)
	cfg.OAuthBaseURL = mock.URL
	store, _ := accounts.NewStore(cfg.CredentialsPath, cfg.CredentialSecret)
	oauth := auth.New(cfg, store)

	_, err := oauth.Start(context.Background())
	if err == nil {
		t.Fatal("expected OAuth start error")
	}
	message := err.Error()
	if !strings.Contains(message, "HTTP 429") || strings.Contains(message, "EOF") {
		t.Fatalf("unexpected OAuth error message: %q", message)
	}
}

func TestAccountPoolRotatesBeforeChatWhenActiveQuotaIsExhausted(t *testing.T) {
	var checkedTokens []string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/billing/balance" {
			http.NotFound(w, r)
			return
		}
		token := r.Header.Get("Authorization")
		checkedTokens = append(checkedTokens, token)
		available := 0
		if token == "Bearer token-two" {
			available = 1000
		}
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"balances": []any{map[string]any{"show_name": "GLM-5.2", "total_units": 1000, "used_units": 1000 - available, "remaining_units": available, "available_units": available}}}})
	}))
	defer mock.Close()

	cfg := testConfig(t)
	cfg.BillingBaseURL = mock.URL + "/billing"
	store, _ := accounts.NewStore(cfg.CredentialsPath, cfg.CredentialSecret)
	_, _ = store.Upsert(accounts.User{UserID: "one"}, "token-one", "")
	_, _ = store.Upsert(accounts.User{UserID: "two"}, "token-two", "")

	loader := upstream.NewLoader(cfg, store)
	pool := accountpool.New(cfg, store, loader, quota.New(cfg))
	model, _ := models.Resolve("glm-5.2")
	selection := pool.Select(context.Background(), model)
	if selection.Account == nil || selection.Account.ID != "two" || !selection.Rotated {
		t.Fatalf("expected exhausted active account to rotate to account two: %+v", selection)
	}
	if store.Active().ID != "two" {
		t.Fatalf("rotated account was not persisted active: %+v", store.Active())
	}
	if len(checkedTokens) != 2 || checkedTokens[0] != "Bearer token-one" || checkedTokens[1] != "Bearer token-two" {
		t.Fatalf("unexpected quota check order: %+v", checkedTokens)
	}
}

func TestAccountPoolRotatesWhenAvailableIsBelowRequestReserve(t *testing.T) {
	var checkedTokens []string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/billing/balance" {
			http.NotFound(w, r)
			return
		}
		token := r.Header.Get("Authorization")
		checkedTokens = append(checkedTokens, token)
		available := 56125
		if token == "Bearer fresh-token" {
			available = 3000000
		}
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"balances": []any{map[string]any{"show_name": "GLM-5.2", "total_units": 3000000, "used_units": 3000000 - available, "remaining_units": available, "available_units": available}}}})
	}))
	defer mock.Close()

	cfg := testConfig(t)
	cfg.BillingBaseURL = mock.URL + "/billing"
	cfg.AccountMinAvailable = 96000
	store, _ := accounts.NewStore(cfg.CredentialsPath, cfg.CredentialSecret)
	_, _ = store.Upsert(accounts.User{UserID: "low"}, "low-token", "")
	_, _ = store.Upsert(accounts.User{UserID: "fresh"}, "fresh-token", "")

	loader := upstream.NewLoader(cfg, store)
	pool := accountpool.New(cfg, store, loader, quota.New(cfg))
	model, _ := models.Resolve("glm-5.2")
	selection := pool.Select(context.Background(), model)
	if selection.Account == nil || selection.Account.User.UserID != "fresh" || !selection.Rotated {
		t.Fatalf("expected low-reserve account to rotate to fresh account: %+v", selection)
	}
	if len(checkedTokens) != 2 || checkedTokens[0] != "Bearer low-token" || checkedTokens[1] != "Bearer fresh-token" {
		t.Fatalf("unexpected quota check order: %+v", checkedTokens)
	}
}
