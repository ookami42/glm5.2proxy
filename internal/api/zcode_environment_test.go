package api

import (
	"net/http/httptest"
	"testing"

	"glm5.2proxy/internal/accounts"
	"glm5.2proxy/internal/zcodeenv"
)

func TestQueueZCodeRefreshSkipsUnavailableLiveRefresh(t *testing.T) {
	server := &Server{zcode: newZCodeBridge()}
	result := &zcodeenv.ApplyResult{
		Account:             accounts.PublicAccount{ID: "one", Label: "Conta 1"},
		LiveRefreshPossible: false,
		LiveRefreshQueued:   true,
		LiveRefreshReason:   "sem bridge",
	}

	commandID := server.queueZCodeRefresh(result)

	if commandID != "" {
		t.Fatalf("unexpected bridge command: %s", commandID)
	}
	if result.LiveRefreshQueued {
		t.Fatal("live refresh should not be queued when bridge refresh is unavailable")
	}
	if command := server.zcode.Next(); command != nil {
		t.Fatalf("unexpected pending bridge command: %+v", command)
	}
}

func TestAccountListIncludesQuotaFlag(t *testing.T) {
	if accountListIncludesQuota(httptest.NewRequest("GET", "/api/admin/accounts", nil)) != true {
		t.Fatal("default account list should include quota for API compatibility")
	}
	for _, target := range []string{
		"/api/admin/accounts?quota=0",
		"/api/admin/accounts?quota=false",
		"/api/admin/accounts?include_quota=skip",
	} {
		if accountListIncludesQuota(httptest.NewRequest("GET", target, nil)) {
			t.Fatalf("%s should skip quota", target)
		}
	}
}

func TestQueueZCodeRefreshQueuesAvailableLiveRefresh(t *testing.T) {
	server := &Server{zcode: newZCodeBridge()}
	result := &zcodeenv.ApplyResult{
		Account:             accounts.PublicAccount{ID: "one", Label: "Conta 1"},
		LiveRefreshPossible: true,
	}

	commandID := server.queueZCodeRefresh(result)

	if commandID == "" {
		t.Fatal("expected bridge command")
	}
	if !result.LiveRefreshQueued {
		t.Fatal("expected live refresh to be marked as queued")
	}
	if command := server.zcode.Next(); command == nil || command.CommandID != commandID || command.AccountID != "one" {
		t.Fatalf("unexpected pending bridge command: %+v", command)
	}
	if !server.zcode.Ack(commandID, true, "ok") {
		t.Fatal("expected queued bridge command to ack")
	}
}
