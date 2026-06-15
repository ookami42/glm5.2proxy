package tests

import (
	"path/filepath"
	"testing"
	"time"

	"glm5.2proxy/internal/config"
)

func testConfig(t *testing.T) config.Config {
	t.Helper()
	dir := t.TempDir()
	return config.Config{
		Host: "127.0.0.1", DefaultPort: 3005, DataDir: dir,
		CredentialsPath: filepath.Join(dir, "accounts.enc.json"), AdminPath: filepath.Join(dir, "admin.json"),
		ModelIODir: filepath.Join(dir, "model-io"), UpstreamURL: "http://127.0.0.1/unused",
		BillingBaseURL: "http://127.0.0.1/unused", OAuthBaseURL: "http://127.0.0.1/unused",
		OAuthProvider: "zai", AppVersion: "3.0.1", Platform: "win32-x64",
		CredentialSecret: "test-secret", CaptchaEnabled: false, CaptchaTimeout: 2 * time.Second,
		CaptchaPreferredClient: "headless-browser", HeadlessEnabled: false,
		HeadlessProfileDir: filepath.Join(dir, "browser"), HeadlessRestartDelay: 10 * time.Millisecond,
		UpstreamTimeout: 5 * time.Second, RetryMaxAttempts: 3, RetryBaseDelay: time.Millisecond, RetryMaxDelay: time.Millisecond,
		AccountRotation: true, AccountMinAvailable: 1, QuotaLog: false,
		QuotaRefreshDelay: time.Millisecond, QuotaRefreshAttempts: 1,
		DefaultMaxTokens: 64000, DefaultThinkingEnabled: true, DefaultThinkingBudget: 32000, DefaultEffort: "max",
	}
}
