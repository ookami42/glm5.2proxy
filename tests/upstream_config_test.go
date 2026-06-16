package tests

import (
	"os"
	"path/filepath"
	"testing"

	"glm5.2proxy/internal/accounts"
	"glm5.2proxy/internal/upstream"
)

func TestUpstreamLoaderClonesOfficialHeadersAndPreservesCapturedSession(t *testing.T) {
	cfg := testConfig(t)
	cfg.Authorization = "Bearer test"
	if err := os.MkdirAll(cfg.ModelIODir, 0o700); err != nil {
		t.Fatal(err)
	}
	record := `{"request":{"headers":{"Authorization":"Bearer captured","X-Title":"Z Code@electron","X-ZCode-Agent":"glm","x-session-id":"official-session"},"body":{"model":"GLM-5.2","stream":true}}}`
	if err := os.WriteFile(filepath.Join(cfg.ModelIODir, "model-io-test.jsonl"), []byte(record+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := accounts.NewStore(cfg.CredentialsPath, cfg.CredentialSecret)
	if err != nil {
		t.Fatal(err)
	}

	loaded := upstream.NewLoader(cfg, store).Load(nil)
	if loaded.BaseHeaders["x-title"] != "Z Code@electron" || loaded.BaseHeaders["x-zcode-agent"] != "glm" {
		t.Fatalf("official ZCode identity headers were not preserved: %+v", loaded.BaseHeaders)
	}
	if loaded.BaseHeaders["x-session-id"] != "official-session" {
		t.Fatalf("captured official session must be preserved for captcha-bound requests: %+v", loaded.BaseHeaders)
	}
}
