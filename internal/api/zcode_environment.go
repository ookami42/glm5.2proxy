package api

import (
	"fmt"
	"net/http"
	"time"

	"glm5.2proxy/internal/accounts"
	"glm5.2proxy/internal/zcodeenv"
)

func (s *Server) zcodeEnvironment(w http.ResponseWriter, _ *http.Request) {
	env := zcodeenv.Detect()
	writeJSON(w, http.StatusOK, map[string]any{"object": "zcode.environment", "data": env})
}

func (s *Server) activateAccountInZCode(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	account := s.accounts.Get(id)
	if account == nil {
		writeError(w, http.StatusNotFound, "account not found", "not_found")
		return
	}
	result, commandID, err := s.applyAccountToZCode(*account, false)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error(), "zcode_environment_apply_failed")
		return
	}
	if result.BridgePatched {
		s.logs.add("info", "zcode.bridge_patched", result.BridgePatchMessage)
	}
	s.logs.add("info", "zcode.account_applied", zcodeApplyLogMessage(result, commandID, "gravada no ambiente interno do ZCode"))
	writeJSON(w, http.StatusOK, map[string]any{"object": "zcode.account_applied", "data": result})
}

func (s *Server) applyAccountInZCode(accountID string) (*zcodeenv.ApplyResult, error) {
	account := s.accounts.Get(accountID)
	if account == nil {
		return nil, nil
	}
	result, commandID, err := s.applyAccountToZCode(*account, true)
	if err != nil {
		s.logs.add("warn", "zcode.account_apply_failed", "Conta "+account.ID+" ativada no proxy, mas nao foi aplicada no ZCode: "+err.Error())
		return nil, err
	}
	if result == nil {
		return nil, nil
	}
	if result.BridgePatched {
		s.logs.add("info", "zcode.bridge_patched", result.BridgePatchMessage)
	}
	s.logs.add("info", "zcode.account_applied", zcodeApplyLogMessage(result, commandID, "sincronizada no disco do ZCode"))
	return result, nil
}

func (s *Server) applyAccountToZCode(account accounts.Account, requireEnvironment bool) (*zcodeenv.ApplyResult, string, error) {
	s.zcodeApplyMu.Lock()
	defer s.zcodeApplyMu.Unlock()

	env := zcodeenv.Detect()
	if requireEnvironment && !zcodeenv.Available(env) {
		s.logs.add("info", "zcode.environment_missing", "Conta ativada no proxy; ambiente interno do ZCode nao foi detectado, entao a sincronizacao foi ignorada")
		return nil, "", nil
	}
	result, err := zcodeenv.ApplyAccountWithBridgeInEnvironment(env, account, fmt.Sprintf("http://127.0.0.1:%d", s.port))
	if err != nil {
		return nil, "", err
	}
	commandID := s.queueZCodeRefresh(&result)
	return &result, commandID, nil
}

func (s *Server) queueZCodeRefresh(result *zcodeenv.ApplyResult) string {
	if result == nil || !result.LiveRefreshPossible {
		if result != nil {
			result.LiveRefreshQueued = false
		}
		return ""
	}
	command := s.zcode.QueueRefresh(result.Account.ID, result.Account.Label)
	result.LiveRefreshQueued = true
	s.scheduleZCodeRefreshFallback(command.CommandID)
	return command.CommandID
}

func (s *Server) scheduleZCodeRefreshFallback(commandID string) {
	go s.finishPendingZCodeRefresh(commandID)
}

func (s *Server) finishPendingZCodeRefresh(commandID string) {
	for range 10 {
		time.Sleep(500 * time.Millisecond)
		if !s.zcode.Pending(commandID) {
			return
		}
	}
	env := zcodeenv.Detect()
	if err := zcodeenv.Restart(env); err != nil {
		s.logs.add("warn", "zcode.bridge_refresh_fallback_failed", "Bridge do ZCode nao confirmou o refresh live e o reinicio automatico falhou: "+err.Error())
		return
	}
	message := "Bridge do ZCode nao confirmou o refresh live; credenciais foram gravadas e o ZCode foi reiniciado para carregar a conta."
	if s.zcode.FallbackRestarted(commandID, message) {
		s.logs.add("info", "zcode.bridge_refresh_fallback_restarted", message)
	}
}

func zcodeApplyLogMessage(result *zcodeenv.ApplyResult, commandID, action string) string {
	if commandID != "" {
		return "Conta " + result.Account.Label + " " + action + "; refresh live enfileirado em " + commandID
	}
	reason := result.LiveRefreshReason
	if reason == "" {
		reason = "bridge live nao disponivel"
	}
	return "Conta " + result.Account.Label + " " + action + "; refresh live nao enfileirado: " + reason
}
