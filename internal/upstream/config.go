package upstream

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"glm5.2proxy/internal/accounts"
	"glm5.2proxy/internal/config"
)

type Config struct {
	Endpoint         string
	BaseHeaders      map[string]string
	BodyTemplate     map[string]any
	Source           string
	ActiveAccount    *accounts.PublicAccount
	HasAuthorization bool
	HasCaptcha       bool
}

type Loader struct {
	cfg      config.Config
	accounts *accounts.Store
}

func NewLoader(cfg config.Config, store *accounts.Store) *Loader {
	return &Loader{cfg: cfg, accounts: store}
}

func (l *Loader) Load(account *accounts.Account) Config {
	record, source := latestModelIO(l.cfg.ModelIODir)
	headers := map[string]string{}
	template := map[string]any(nil)
	endpoint := l.cfg.UpstreamURL
	if request, ok := record["request"].(map[string]any); ok {
		if captured, ok := request["headers"].(map[string]any); ok {
			for key, value := range captured {
				if text, ok := value.(string); ok {
					headers[key] = text
				}
			}
		}
		if body, ok := request["body"].(map[string]any); ok {
			template = body
		}
		if provider, ok := request["providerOptions"].(map[string]any); ok {
			if endpoints, ok := provider["endpoints"].(map[string]any); ok && os.Getenv("ZCODE_UPSTREAM_URL") == "" {
				base, _ := endpoints["baseURL"].(string)
				paths, _ := endpoints["paths"].(map[string]any)
				anthropic, _ := paths["anthropic"].(string)
				if base != "" && anthropic != "" {
					endpoint = strings.TrimRight(base, "/") + anthropic
				}
			}
		}
	}
	if account == nil {
		account = l.accounts.Active()
	}
	authorization := l.cfg.Authorization
	activeSource := ""
	var public *accounts.PublicAccount
	if authorization == "" && account != nil {
		authorization = accounts.Authorization(account)
		item := accounts.Sanitize(*account)
		public = &item
		activeSource = "proxy-account:" + account.ID
	}
	if authorization == "" {
		authorization = header(headers, "Authorization")
	}
	if activeSource == "" {
		if l.cfg.Authorization != "" {
			activeSource = "environment"
		} else {
			activeSource = source
		}
	}
	captcha := first(os.Getenv("ZCODE_CAPTCHA_VERIFY_PARAM"), header(headers, "X-Aliyun-Captcha-Verify-Param"))
	baseHeaders := map[string]string{
		"authorization":       authorization,
		"http-referer":        first(os.Getenv("ZCODE_HTTP_REFERER"), header(headers, "HTTP-Referer"), "https://zcode.z.ai"),
		"user-agent":          first(os.Getenv("ZCODE_USER_AGENT"), header(headers, "User-Agent"), "ZCode/"+l.cfg.AppVersion),
		"x-zcode-app-version": first(os.Getenv("ZCODE_APP_VERSION"), header(headers, "X-ZCode-App-Version"), l.cfg.AppVersion),
		"x-title":             first(os.Getenv("ZCODE_TITLE"), header(headers, "X-Title"), "Z Code@electron"),
		"x-zcode-agent":       first(os.Getenv("ZCODE_AGENT"), header(headers, "X-ZCode-Agent"), "glm"),
		"x-session-id":        os.Getenv("ZCODE_SESSION_ID"),
	}
	if captcha != "" {
		baseHeaders["x-aliyun-captcha-verify-param"] = captcha
	}
	for key, value := range baseHeaders {
		if value == "" {
			delete(baseHeaders, key)
		}
	}
	return Config{Endpoint: endpoint, BaseHeaders: baseHeaders, BodyTemplate: template, Source: activeSource, ActiveAccount: public, HasAuthorization: authorization != "", HasCaptcha: captcha != ""}
}

func latestModelIO(directory string) (map[string]any, string) {
	files, _ := filepath.Glob(filepath.Join(directory, "model-io-*.jsonl"))
	sort.Slice(files, func(i, j int) bool {
		left, _ := os.Stat(files[i])
		right, _ := os.Stat(files[j])
		return left != nil && right != nil && left.ModTime().After(right.ModTime())
	})
	for _, file := range files {
		handle, err := os.Open(file)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(handle)
		buffer := make([]byte, 64*1024)
		scanner.Buffer(buffer, 32*1024*1024)
		for scanner.Scan() {
			var record map[string]any
			if json.Unmarshal(scanner.Bytes(), &record) != nil {
				continue
			}
			request, _ := record["request"].(map[string]any)
			headers, _ := request["headers"].(map[string]any)
			if headerAny(headers, "Authorization") != "" {
				handle.Close()
				return record, file
			}
		}
		handle.Close()
	}
	return map[string]any{}, ""
}

func header(values map[string]string, target string) string {
	for key, value := range values {
		if strings.EqualFold(key, target) {
			return value
		}
	}
	return ""
}

func headerAny(values map[string]any, target string) string {
	for key, value := range values {
		if strings.EqualFold(key, target) {
			text, _ := value.(string)
			return text
		}
	}
	return ""
}

func first(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
