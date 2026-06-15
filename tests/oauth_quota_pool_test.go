package tests

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/oauth/cli/init":
			json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": map[string]any{"flow_id": "flow-1", "authorize_url": "https://example.test/login", "poll_interval_sec": 1}})
		case r.URL.Path == "/oauth/cli/poll/flow-1":
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
