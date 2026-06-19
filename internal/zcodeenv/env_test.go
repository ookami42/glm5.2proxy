package zcodeenv

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"glm5.2proxy/internal/accounts"
)

func TestCipherRoundTrip(t *testing.T) {
	t.Setenv("ZCODE_CREDENTIAL_SECRET", "test-secret")
	cipher := NewCipher(t.TempDir())
	encrypted, err := cipher.Encrypt("valor secreto")
	if err != nil {
		t.Fatal(err)
	}
	if encrypted == "valor secreto" {
		t.Fatal("expected encrypted value")
	}
	decrypted, err := cipher.Decrypt(encrypted)
	if err != nil {
		t.Fatal(err)
	}
	if decrypted != "valor secreto" {
		t.Fatalf("unexpected decrypted value: %q", decrypted)
	}
}

func TestWriteCredentialsPreservesOtherKeys(t *testing.T) {
	t.Setenv("ZCODE_CREDENTIAL_SECRET", "test-secret")
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")
	if err := os.WriteFile(path, []byte(`{"other":"value"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	account := accounts.Account{
		User:            accounts.User{UserID: "u1", Email: "u1@example.com", Name: "User 1"},
		ZCodeJWTToken:   "jwt-token",
		ZAIAcccessToken: "access-token",
	}
	backup, err := writeCredentials(path, NewCipher(dir), account)
	if err != nil {
		t.Fatal(err)
	}
	if backup == "" {
		t.Fatal("expected backup path")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]string
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if saved["other"] != "value" {
		t.Fatal("other credential key was not preserved")
	}
	plain, err := NewCipher(dir).Decrypt(saved[credentialJWTToken])
	if err != nil {
		t.Fatal(err)
	}
	if plain != "jwt-token" {
		t.Fatalf("unexpected jwt: %q", plain)
	}
}

func TestWriteCredentialsAllowsJWTOnlyAccount(t *testing.T) {
	t.Setenv("ZCODE_CREDENTIAL_SECRET", "test-secret")
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials.json")
	account := accounts.Account{
		User:          accounts.User{UserID: "u1", Email: "u1@example.com", Name: "User 1"},
		ZCodeJWTToken: "jwt-token",
	}
	if _, err := writeCredentials(path, NewCipher(dir), account); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]string
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if saved[credentialAccessToken] != "" {
		t.Fatal("did not expect access token credential for jwt-only account")
	}
	if saved[credentialJWTToken] == "" {
		t.Fatal("expected jwt credential")
	}
}

func TestDetectBridgeProxyBaseURL(t *testing.T) {
	raw := []byte(`;(()=>{if(globalThis.__GLM52_PROXY_BRIDGE__)return;const u="http://127.0.0.1:34115/api/admin/zcode/bridge";})();`)
	got := detectBridgeProxyBaseURL(raw)
	if got != "http://127.0.0.1:34115" {
		t.Fatalf("unexpected bridge proxy base URL: %q", got)
	}
}

func TestUpdateConfigWritesBothZCodeCodingProviders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{
  "modelProviders": {
    "builtin:zai-start-plan": {
      "enabled": false,
      "options": {
        "baseURL": "https://custom.example.test/anthropic",
        "other": "preserved"
      }
    }
  }
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := updateConfig(path, "jwt-token"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved map[string]any
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	providers := saved["modelProviders"].(map[string]any)
	for _, providerID := range codingPlanProviderIDs {
		provider := providers[providerID].(map[string]any)
		if provider["enabled"] != true {
			t.Fatalf("%s was not enabled", providerID)
		}
		options := provider["options"].(map[string]any)
		if options["apiKey"] != "jwt-token" {
			t.Fatalf("%s apiKey was not updated", providerID)
		}
		if options["baseURL"] == "" {
			t.Fatalf("%s baseURL was not set", providerID)
		}
	}
	startOptions := providers["builtin:zai-start-plan"].(map[string]any)["options"].(map[string]any)
	if startOptions["other"] != "preserved" {
		t.Fatal("existing provider options were not preserved")
	}
	if startOptions["baseURL"] != "https://custom.example.test/anthropic" {
		t.Fatal("existing provider baseURL was overwritten")
	}
}
