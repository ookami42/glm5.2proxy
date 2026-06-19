package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"glm5.2proxy/internal/accounts"
	"glm5.2proxy/internal/config"
)

type Flow struct {
	FlowID          string     `json:"flowId"`
	AuthorizeURL    string     `json:"authorizeUrl"`
	ExpiresAt       *time.Time `json:"expiresAt"`
	PollIntervalSec int        `json:"pollIntervalSec"`
	Status          string     `json:"status"`
	pollToken       string
}

type Service struct {
	cfg      config.Config
	accounts *accounts.Store
	client   *http.Client
	mu       sync.RWMutex
	flows    map[string]*Flow
}

func New(cfg config.Config, store *accounts.Store) *Service {
	return &Service{cfg: cfg, accounts: store, client: &http.Client{Timeout: 15 * time.Second}, flows: map[string]*Flow{}}
}

func (s *Service) Start(ctx context.Context) (Flow, error) {
	pollToken := randomHex(32)
	var payload struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			FlowID          string `json:"flow_id"`
			PollToken       string `json:"poll_token"`
			AuthorizeURL    string `json:"authorize_url"`
			ExpiresAt       int64  `json:"expires_at"`
			PollIntervalSec int    `json:"poll_interval_sec"`
		} `json:"data"`
	}
	if err := s.request(ctx, http.MethodPost, "/oauth/cli/init", pollToken, map[string]any{"provider": s.cfg.OAuthProvider}, &payload); err != nil {
		return Flow{}, err
	}
	if payload.Data.FlowID == "" || payload.Data.AuthorizeURL == "" {
		return Flow{}, errors.New("OAuth init response is missing flow_id or authorize_url")
	}
	var expires *time.Time
	if payload.Data.ExpiresAt > 0 {
		value := time.Unix(payload.Data.ExpiresAt, 0).UTC()
		expires = &value
	}
	authorizeURL := strings.Replace(payload.Data.AuthorizeURL, "/api/oauth/authorize", "/auth/oauth/authorize", 1)
	flow := Flow{FlowID: payload.Data.FlowID, AuthorizeURL: authorizeURL, ExpiresAt: expires, PollIntervalSec: payload.Data.PollIntervalSec, Status: "pending", pollToken: first(payload.Data.PollToken, pollToken)}
	if flow.PollIntervalSec == 0 {
		flow.PollIntervalSec = 2
	}
	s.mu.Lock()
	s.flows[flow.FlowID] = &flow
	s.mu.Unlock()
	return publicFlow(flow), nil
}

func (s *Service) Poll(ctx context.Context, flowID string) (map[string]any, error) {
	s.mu.RLock()
	flow := s.flows[flowID]
	s.mu.RUnlock()
	if flow == nil {
		return nil, errors.New("unknown OAuth flow; start login again")
	}
	var payload struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Status string        `json:"status"`
			Token  string        `json:"token"`
			User   accounts.User `json:"user"`
			ZAI    struct {
				AccessToken string `json:"access_token"`
			} `json:"zai"`
		} `json:"data"`
	}
	if err := s.request(ctx, http.MethodGet, "/oauth/cli/poll/"+url.PathEscape(flowID), flow.pollToken, nil, &payload); err != nil {
		return nil, err
	}
	flow.Status = first(payload.Data.Status, "pending")
	result := map[string]any{"flowId": flow.FlowID, "authorizeUrl": flow.AuthorizeURL, "expiresAt": flow.ExpiresAt, "pollIntervalSec": flow.PollIntervalSec, "status": flow.Status}
	if flow.Status == "ready" {
		account, err := s.accounts.Upsert(payload.Data.User, payload.Data.Token, payload.Data.ZAI.AccessToken)
		if err != nil {
			return nil, err
		}
		result["account"] = account
		s.mu.Lock()
		delete(s.flows, flowID)
		s.mu.Unlock()
	} else if flow.Status == "failed" {
		s.mu.Lock()
		delete(s.flows, flowID)
		s.mu.Unlock()
	}
	return result, nil
}

func (s *Service) Status() []Flow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Flow, 0, len(s.flows))
	for _, flow := range s.flows {
		result = append(result, publicFlow(*flow))
	}
	return result
}

func (s *Service) request(ctx context.Context, method, path, bearer string, body any, target any) error {
	var content *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		content = bytes.NewReader(raw)
	} else {
		content = bytes.NewReader(nil)
	}
	request, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(s.cfg.OAuthBaseURL, "/")+path, content)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "Bearer "+bearer)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := s.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		return err
	}
	raw, _ := json.Marshal(target)
	var status struct {
		Code    int    `json:"code"`
		Msg     string `json:"msg"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(raw, &status)
	if response.StatusCode < 200 || response.StatusCode >= 300 || status.Code != 0 {
		return fmt.Errorf("OAuth request failed: HTTP %d %s", response.StatusCode, first(status.Msg, status.Message))
	}
	return nil
}

func publicFlow(flow Flow) Flow {
	flow.pollToken = ""
	return flow
}

func randomHex(size int) string {
	value := make([]byte, size)
	_, _ = rand.Read(value)
	return hex.EncodeToString(value)
}

func first(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
