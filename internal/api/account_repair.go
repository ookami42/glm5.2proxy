package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"glm5.2proxy/internal/accounts"
	"glm5.2proxy/internal/codingplan"
	"glm5.2proxy/internal/quota"
	"glm5.2proxy/internal/upstream"
)

type codingPlanRefreshOutcome struct {
	Account           accounts.PublicAccount `json:"account"`
	Result            codingplan.Result      `json:"data"`
	CredentialStored  bool                   `json:"credentialStored"`
	StartPlanSnapshot quota.Snapshot         `json:"startPlanSnapshot,omitempty"`
}

type accountRepairReport struct {
	Object   string              `json:"object"`
	Trigger  string              `json:"trigger"`
	Started  time.Time           `json:"startedAt"`
	Duration string              `json:"duration"`
	Total    int                 `json:"total"`
	Healthy  int                 `json:"healthy"`
	Repaired int                 `json:"repaired"`
	Removed  int                 `json:"removed"`
	Skipped  int                 `json:"skipped"`
	Failed   int                 `json:"failed"`
	Items    []accountRepairItem `json:"items"`
}

type accountRepairItem struct {
	Account         accounts.PublicAccount `json:"account"`
	Action          string                 `json:"action"`
	Reason          string                 `json:"reason,omitempty"`
	Error           string                 `json:"error,omitempty"`
	BalanceCount    int                    `json:"balanceCount"`
	QuotaVerified   bool                   `json:"quotaVerified"`
	StartPlanOK     bool                   `json:"startPlanVerified"`
	CredentialSaved bool                   `json:"credentialSaved"`
}

func (s *Server) repairAccounts(w http.ResponseWriter, r *http.Request) {
	report := s.RepairBrokenAccounts(r.Context(), "manual")
	writeJSON(w, http.StatusOK, report)
}

func (s *Server) RepairBrokenAccounts(ctx context.Context, trigger string) accountRepairReport {
	started := time.Now()
	report := accountRepairReport{Object: "zcode.account_repair", Trigger: trigger, Started: started, Items: []accountRepairItem{}}
	if !s.repairMu.TryLock() {
		s.logs.add("warn", "account_repair.skipped_already_running", "Manutencao de contas ignorada porque outra varredura ja esta em andamento")
		report.Skipped = 1
		report.Duration = time.Since(started).Round(time.Millisecond).String()
		return report
	}
	defer s.repairMu.Unlock()

	savedAccounts := s.accounts.Accounts()
	report.Total = len(savedAccounts)
	s.logs.add("info", "account_repair.started", fmt.Sprintf("Manutencao de contas iniciada por %s; avaliando %d conta(s)", trigger, len(savedAccounts)))
	for _, account := range savedAccounts {
		select {
		case <-ctx.Done():
			report.Failed++
			report.Items = append(report.Items, accountRepairItem{Account: accounts.Sanitize(account), Action: "cancelled", Error: ctx.Err().Error()})
			s.logs.add("warn", "account_repair.cancelled", "Manutencao de contas cancelada: "+ctx.Err().Error())
			report.Duration = time.Since(started).Round(time.Millisecond).String()
			return report
		default:
		}
		item := s.repairOneAccount(ctx, account)
		report.Items = append(report.Items, item)
		switch item.Action {
		case "healthy":
			report.Healthy++
		case "repaired":
			report.Repaired++
		case "removed":
			report.Removed++
		case "skipped":
			report.Skipped++
		default:
			report.Failed++
		}
	}
	report.Duration = time.Since(started).Round(time.Millisecond).String()
	s.logs.add("info", "account_repair.completed", fmt.Sprintf("Manutencao de contas concluida em %s: saudaveis=%d reparadas=%d removidas=%d ignoradas=%d falhas=%d", report.Duration, report.Healthy, report.Repaired, report.Removed, report.Skipped, report.Failed))
	return report
}

func (s *Server) repairOneAccount(ctx context.Context, account accounts.Account) accountRepairItem {
	public := accounts.Sanitize(account)
	item := accountRepairItem{Account: public}
	s.logs.add("info", "account_repair.account_started", fmt.Sprintf("Verificando %s (%s)", public.Label, public.User.Email))

	before, beforeErr := s.startPlanSnapshot(ctx, account)
	if beforeErr == nil && hasQuotaBalances(before) {
		item.Action = "healthy"
		item.Reason = "Start Plan ja possui balances"
		item.BalanceCount = len(before.Balances)
		item.StartPlanOK = true
		s.logs.add("info", "account_repair.account_healthy", fmt.Sprintf("%s mantida: Start Plan retornou %d balance(s)", public.Label, len(before.Balances)))
		return item
	}
	if beforeErr != nil {
		item.Error = beforeErr.Error()
		s.logs.add("warn", "account_repair.quota_check_failed", fmt.Sprintf("%s falhou ao consultar Start Plan antes do reparo: %s", public.Label, beforeErr))
		if transientAccountRepairError(beforeErr) {
			item.Action = "skipped"
			item.Reason = "erro temporario ao consultar cota; nao removi a conta"
			s.logs.add("warn", "account_repair.account_skipped_transient", fmt.Sprintf("%s nao foi removida porque a falha parece temporaria: %s", public.Label, beforeErr))
			return item
		}
	} else {
		s.logs.add("warn", "account_repair.empty_quota", fmt.Sprintf("%s esta sem balances de Start Plan; tentando reparar direto pelo proxy", public.Label))
	}

	if strings.TrimSpace(account.ZAIAcccessToken) == "" {
		return s.keepBrokenAccount(item, "conta sem balances de Start Plan e sem ZAI access token salvo para reparo")
	}
	outcome, err := s.refreshCodingPlanForAccount(ctx, account)
	if err != nil {
		item.Error = err.Error()
		s.logs.add("warn", "account_repair.repair_failed", fmt.Sprintf("%s falhou no reparo direto: %s", public.Label, err))
		if transientAccountRepairError(err) {
			item.Action = "skipped"
			item.Reason = "erro temporario no reparo direto; nao removi a conta"
			return item
		}
		return s.keepBrokenAccount(item, "reparo direto falhou e a conta continuava sem cota: "+err.Error())
	}
	item.QuotaVerified = outcome.Result.QuotaVerified
	item.StartPlanOK = outcome.Result.StartPlanVerified
	item.CredentialSaved = outcome.CredentialStored
	item.BalanceCount = len(outcome.StartPlanSnapshot.Balances)
	if outcome.Result.StartPlanVerified && hasQuotaBalances(outcome.StartPlanSnapshot) {
		item.Action = "repaired"
		item.Reason = "Start Plan voltou a retornar balances depois do reparo"
		s.logs.add("info", "account_repair.account_repaired", fmt.Sprintf("%s reparada: Start Plan retornou %d balance(s); quota_verified=%t credential_saved=%t", public.Label, len(outcome.StartPlanSnapshot.Balances), outcome.Result.QuotaVerified, outcome.CredentialStored))
		return item
	}
	reason := "reparo direto terminou, mas Start Plan continuou sem balances"
	if outcome.Result.StartPlanError != "" {
		reason += ": " + outcome.Result.StartPlanError
	}
	return s.keepBrokenAccount(item, reason)
}

func (s *Server) refreshCodingPlanForAccount(ctx context.Context, account accounts.Account) (codingPlanRefreshOutcome, error) {
	outcome := codingPlanRefreshOutcome{Account: accounts.Sanitize(account)}
	result, err := s.codingPlan.Refresh(ctx, account)
	if err != nil {
		return outcome, err
	}
	if result.Credential != "" {
		quotaSnapshot, err := s.quota.BalanceSnapshot(ctx, upstream.Config{
			QuotaEndpoint:      s.cfg.ZAIUsageQuotaURL,
			QuotaAuthorization: result.Credential,
			BaseHeaders:        map[string]string{"user-agent": "ZCode/" + s.cfg.AppVersion},
			HasAuthorization:   true,
		})
		if err != nil || len(quotaSnapshot.Balances) == 0 {
			if err != nil {
				result.QuotaError = err.Error()
			} else {
				result.QuotaError = "coding plan quota returned no balances"
			}
			_, _ = s.accounts.UpdateCodingPlanAPIKey(account.ID, "")
		} else {
			result.QuotaVerified = true
			if _, err := s.accounts.UpdateCodingPlanAPIKey(account.ID, result.Credential); err != nil {
				return outcome, err
			}
			outcome.CredentialStored = true
		}
	}
	startPlanSnapshot, err := s.startPlanSnapshot(ctx, account)
	if err != nil {
		result.StartPlanError = err.Error()
	} else {
		outcome.StartPlanSnapshot = startPlanSnapshot
		result.StartPlanVerified = hasQuotaBalances(startPlanSnapshot)
		if !result.StartPlanVerified {
			result.StartPlanError = "start plan returned no balances"
		}
	}
	outcome.Result = result
	return outcome, nil
}

func (s *Server) startPlanSnapshot(ctx context.Context, account accounts.Account) (quota.Snapshot, error) {
	startPlanAccount := account
	startPlanAccount.CodingPlanAPIKey = ""
	return s.quota.Snapshot(ctx, s.loader.Load(&startPlanAccount))
}

func hasQuotaBalances(snapshot quota.Snapshot) bool {
	return len(snapshot.Balances) > 0
}

func (s *Server) keepBrokenAccount(item accountRepairItem, reason string) accountRepairItem {
	item.Action = "skipped"
	item.Reason = reason
	s.logs.add("warn", "account_repair.account_preserved", fmt.Sprintf("%s mantida no pool; manutencao automatica nao remove contas. Motivo detectado: %s", item.Account.Label, reason))
	return item
}

func transientAccountRepairError(err error) bool {
	if err == nil {
		return false
	}
	value := strings.ToLower(err.Error())
	for _, marker := range []string{"context deadline", "context canceled", "timeout", "temporarily", "connection reset", "server closed idle connection", "http 429", "too many requests", "http 500", "http 502", "http 503", "http 504"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}
