package accountcreator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"glm5.2proxy/internal/config"
)

type Runner struct {
	cfg     config.Config
	mu      sync.Mutex
	lastRun time.Time
}

type Result struct {
	Enabled   bool   `json:"enabled"`
	Started   bool   `json:"started"`
	Command   string `json:"command,omitempty"`
	WorkDir   string `json:"workDir,omitempty"`
	Output    string `json:"output,omitempty"`
	Duration  string `json:"duration,omitempty"`
	Username  string `json:"username,omitempty"`
	Email     string `json:"email,omitempty"`
	AccountID string `json:"accountId,omitempty"`
	Label     string `json:"label,omitempty"`
}

func New(cfg config.Config) *Runner {
	return &Runner{cfg: cfg}
}

func (r *Runner) Enabled() bool {
	return r != nil && r.cfg.AccountCreatorEnabled && strings.TrimSpace(r.cfg.AccountCreatorDir) != ""
}

func (r *Runner) Run(ctx context.Context, proxyBaseURL string) (Result, error) {
	result := Result{Enabled: r.Enabled(), WorkDir: r.cfg.AccountCreatorDir}
	if !r.Enabled() {
		return result, errors.New("criacao automatica de contas desativada")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if wait := r.cooldownRemaining(); wait > 0 {
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return result, ctx.Err()
		case <-timer.C:
		}
	}
	if err := validateWorkDir(r.cfg.AccountCreatorDir); err != nil {
		return result, err
	}
	started := time.Now()
	runCtx := ctx
	cancel := func() {}
	if r.cfg.AccountCreatorTimeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, r.cfg.AccountCreatorTimeout)
	}
	defer cancel()

	command := strings.TrimSpace(r.cfg.AccountCreatorCommand)
	if command == "" {
		command = "node"
	}
	cmd := exec.CommandContext(runCtx, command, "src/main.js", "1")
	cmd.Dir = r.cfg.AccountCreatorDir
	cmd.Env = append(os.Environ(),
		"ZCODE_PROXY_AUTO_LINK=1",
		"ZCODE_PROXY_BASE_URL="+proxyBaseURL,
	)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	r.lastRun = time.Now()
	result.Started = true
	result.Command = command + " src/main.js 1"
	result.Output = trimOutput(output.String())
	result.Duration = time.Since(started).Round(time.Millisecond).String()
	applyAutomationSummary(&result, output.String())
	if err != nil {
		if runCtx.Err() != nil {
			return result, fmt.Errorf("criacao automatica de conta excedeu o timeout: %w", runCtx.Err())
		}
		return result, fmt.Errorf("criacao automatica de conta falhou: %w", err)
	}
	return result, nil
}

func applyAutomationSummary(result *Result, output string) {
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "=== Conta criada com sucesso ===") {
			var payload struct {
				Username string `json:"username"`
				Email    string `json:"email"`
			}
			if decodeLogPayload(line, &payload) == nil {
				result.Username = payload.Username
				result.Email = payload.Email
			}
		}
		if strings.Contains(line, "Conta vinculada ao proxy GLM5.2") {
			var payload struct {
				Email     string `json:"email"`
				AccountID string `json:"accountId"`
				Label     string `json:"label"`
			}
			if decodeLogPayload(line, &payload) == nil {
				if payload.Email != "" {
					result.Email = payload.Email
				}
				result.AccountID = payload.AccountID
				result.Label = payload.Label
			}
		}
	}
}

func decodeLogPayload(line string, target any) error {
	start := strings.LastIndex(line, "{")
	if start < 0 {
		return errors.New("log line has no JSON payload")
	}
	return json.Unmarshal([]byte(strings.TrimSpace(line[start:])), target)
}

func (r *Runner) cooldownRemaining() time.Duration {
	if r.cfg.AccountCreatorCooldown <= 0 || r.lastRun.IsZero() {
		return 0
	}
	wait := r.cfg.AccountCreatorCooldown - time.Since(r.lastRun)
	if wait < 0 {
		return 0
	}
	return wait
}

func validateWorkDir(dir string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("pasta da automacao de contas indisponivel %q: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("pasta da automacao de contas nao e diretorio: %s", dir)
	}
	entry := filepath.Join(dir, "src", "main.js")
	if _, err := os.Stat(entry); err != nil {
		return fmt.Errorf("entrada da automacao nao encontrada %q: %w", entry, err)
	}
	return nil
}

func trimOutput(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 4000 {
		return value
	}
	return value[len(value)-4000:]
}
