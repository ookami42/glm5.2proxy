package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Host                   string
	DefaultPort            int
	DataDir                string
	CredentialsPath        string
	AdminPath              string
	ModelIODir             string
	UpstreamURL            string
	BillingBaseURL         string
	OAuthBaseURL           string
	OAuthProvider          string
	AppVersion             string
	Platform               string
	Authorization          string
	CredentialSecret       string
	CaptchaEnabled         bool
	CaptchaTimeout         time.Duration
	CaptchaPreferredClient string
	HeadlessEnabled        bool
	HeadlessExecutable     string
	HeadlessProfileDir     string
	HeadlessRestartDelay   time.Duration
	UpstreamTimeout        time.Duration
	RetryMaxAttempts       int
	RetryBaseDelay         time.Duration
	RetryMaxDelay          time.Duration
	AccountRotation        bool
	AccountMinAvailable    int64
	QuotaLog               bool
	QuotaRefreshDelay      time.Duration
	QuotaRefreshAttempts   int
	DefaultMaxTokens       int
	DefaultThinkingEnabled bool
	DefaultThinkingBudget  int
	DefaultEffort          string
}

func Load() Config {
	home, _ := os.UserHomeDir()
	defaultDataDir := filepath.Join(home, ".glm5.2proxy")
	dataDir := env("ZCODE_PROXY_DATA_DIR", defaultDataDir)
	if os.Getenv("ZCODE_PROXY_DATA_DIR") == "" && os.Getenv("ZCODE_PROXY_CREDENTIALS_PATH") == "" {
		migrateLegacyDataDir(home, dataDir)
	}
	return Config{
		Host:                   env("ZCODE_PROXY_HOST", "127.0.0.1"),
		DefaultPort:            envInt("PORT", envInt("ZCODE_PROXY_PORT", 3005)),
		DataDir:                dataDir,
		CredentialsPath:        env("ZCODE_PROXY_CREDENTIALS_PATH", filepath.Join(dataDir, "zcode-accounts.enc.json")),
		AdminPath:              env("ZCODE_PROXY_ADMIN_PATH", filepath.Join(dataDir, "admin.json")),
		ModelIODir:             env("ZCODE_MODEL_IO_DIR", filepath.Join(home, ".zcode", "cli", "rollout")),
		UpstreamURL:            env("ZCODE_UPSTREAM_URL", "https://zcode.z.ai/api/v1/zcode-plan/anthropic/v1/messages"),
		BillingBaseURL:         env("ZCODE_BILLING_BASE_URL", "https://zcode.z.ai/api/v1/zcode-plan/billing"),
		OAuthBaseURL:           env("ZCODE_OAUTH_BASE_URL", "https://zcode.z.ai/api/v1"),
		OAuthProvider:          env("ZCODE_OAUTH_PROVIDER", "zai"),
		AppVersion:             env("ZCODE_APP_VERSION", "3.0.1"),
		Platform:               env("ZCODE_PLATFORM", platform()),
		Authorization:          os.Getenv("ZCODE_AUTHORIZATION"),
		CredentialSecret:       os.Getenv("ZCODE_PROXY_CREDENTIAL_SECRET"),
		CaptchaEnabled:         enabled("ZCODE_CAPTCHA_BRIDGE", true),
		CaptchaTimeout:         envDurationMS("ZCODE_CAPTCHA_BRIDGE_TIMEOUT_MS", 120000),
		CaptchaPreferredClient: env("ZCODE_CAPTCHA_CLIENT_PREFERENCE", "headless-browser"),
		HeadlessEnabled:        enabled("ZCODE_CAPTCHA_HEADLESS", true),
		HeadlessExecutable:     os.Getenv("ZCODE_CAPTCHA_HEADLESS_EXECUTABLE"),
		HeadlessProfileDir:     env("ZCODE_CAPTCHA_HEADLESS_PROFILE_DIR", filepath.Join(dataDir, "captcha-headless-profile")),
		HeadlessRestartDelay:   envDurationMS("ZCODE_CAPTCHA_HEADLESS_RESTART_DELAY_MS", 3000),
		UpstreamTimeout:        envDurationMS("ZCODE_UPSTREAM_TIMEOUT_MS", 180000),
		RetryMaxAttempts:       envInt("ZCODE_RETRY_MAX_ATTEMPTS", 11),
		RetryBaseDelay:         envDurationMS("ZCODE_RETRY_BASE_DELAY_MS", 1000),
		RetryMaxDelay:          envDurationMS("ZCODE_RETRY_MAX_DELAY_MS", 60000),
		AccountRotation:        enabled("ZCODE_ACCOUNT_ROTATION", true),
		AccountMinAvailable:    int64(envInt("ZCODE_ACCOUNT_MIN_AVAILABLE_UNITS", 1)),
		QuotaLog:               enabled("ZCODE_QUOTA_LOG", true),
		QuotaRefreshDelay:      envDurationMS("ZCODE_QUOTA_REFRESH_DELAY_MS", 1500),
		QuotaRefreshAttempts:   envInt("ZCODE_QUOTA_REFRESH_ATTEMPTS", 3),
		DefaultMaxTokens:       envInt("ZCODE_MAX_TOKENS", 64000),
		DefaultThinkingEnabled: enabled("ZCODE_THINKING", true),
		DefaultThinkingBudget:  envInt("ZCODE_THINKING_BUDGET", 32000),
		DefaultEffort:          env("ZCODE_EFFORT", "max"),
	}
}

func migrateLegacyDataDir(home, dataDir string) {
	if home == "" || dataDir == "" {
		return
	}
	legacyDir := filepath.Join(home, ".kimiproxyplus")
	if samePath(legacyDir, dataDir) {
		return
	}
	copyIfMissing(filepath.Join(legacyDir, "zcode-accounts.enc.json"), filepath.Join(dataDir, "zcode-accounts.enc.json"), 0o600)
	copyIfMissing(filepath.Join(legacyDir, "admin.json"), filepath.Join(dataDir, "admin.json"), 0o600)
}

func copyIfMissing(src, dst string, mode os.FileMode) {
	if _, err := os.Stat(dst); err == nil {
		return
	}
	raw, err := os.ReadFile(src)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(dst, raw, mode)
}

func samePath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	if leftErr == nil {
		left = leftAbs
	}
	if rightErr == nil {
		right = rightAbs
	}
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil {
		return fallback
	}
	return value
}

func envDurationMS(key string, fallback int) time.Duration {
	return time.Duration(envInt(key, fallback)) * time.Millisecond
}

func enabled(key string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	return value != "disabled" && value != "false" && value != "0"
}

func platform() string {
	if os.PathSeparator == '\\' {
		return "win32-x64"
	}
	return "linux-x64"
}
