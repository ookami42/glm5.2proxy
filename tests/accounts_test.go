package tests

import (
	"os"
	"strings"
	"testing"

	"glm5.2proxy/internal/accounts"
)

func TestAccountStoreEncryptionOrderingAndActivation(t *testing.T) {
	cfg := testConfig(t)
	store, err := accounts.NewStore(cfg.CredentialsPath, cfg.CredentialSecret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Upsert(accounts.User{UserID: "one", Email: "one@example.test"}, "token-one", "zai-one"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Upsert(accounts.User{UserID: "two", Email: "two@example.test"}, "token-two", "zai-two"); err != nil {
		t.Fatal(err)
	}
	active, listed := store.Public()
	if active != "one" || listed[0].Label != "Conta 1" || listed[1].Label != "Conta 2" {
		t.Fatalf("unexpected account order: active=%s accounts=%+v", active, listed)
	}
	if _, err := store.Activate("two"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(cfg.CredentialsPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "token-one") || strings.Contains(string(raw), "zai-two") {
		t.Fatal("credential file leaked plaintext secrets")
	}
	reloaded, err := accounts.NewStore(cfg.CredentialsPath, cfg.CredentialSecret)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Active().ID != "two" || len(reloaded.Accounts()) != 2 {
		t.Fatalf("store did not survive reload: %+v", reloaded.Active())
	}
}

func TestAccountStoreReorderPersistsQueueOrder(t *testing.T) {
	cfg := testConfig(t)
	store, err := accounts.NewStore(cfg.CredentialsPath, cfg.CredentialSecret)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"one", "two", "three"} {
		if _, err := store.Upsert(accounts.User{UserID: id}, "token-"+id, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Reorder([]string{"three", "one", "two"}); err != nil {
		t.Fatal(err)
	}
	_, public := store.Public()
	if public[0].ID != "three" || public[0].QueuePosition != 1 || public[0].Label != "Conta 1" {
		t.Fatalf("unexpected reordered queue: %+v", public)
	}
	reloaded, err := accounts.NewStore(cfg.CredentialsPath, cfg.CredentialSecret)
	if err != nil {
		t.Fatal(err)
	}
	got := reloaded.Accounts()
	if got[0].ID != "three" || got[1].ID != "one" || got[2].ID != "two" {
		t.Fatalf("reordered queue was not persisted: %+v", got)
	}
}
