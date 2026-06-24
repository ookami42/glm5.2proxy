package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"glm5.2proxy/internal/accounts"
	"glm5.2proxy/internal/config"
)

type Flow struct {
	FlowID       string     `json:"flowId"`
	AuthorizeURL string     `json:"authorizeUrl"`
	State        string     `json:"state"`
	Status       string     `json:"status"`
	ExpiresAt    *time.Time `json:"expiresAt"`
}

type tokenResponse struct {
	Code int `json:"code"`
	Data struct {
		Token        string `json:"token"`
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresAt    int64  `json:"expires_at"`
	} `json:"data"`
	Msg     string `json:"msg"`
	Message string `json:"message"`
}

type userInfoResponse struct {
	ID       string `json:"id"`
	UserID   string `json:"user_id"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	Nickname string `json:"nickname"`
	Avatar   string `json:"avatar"`
	AvatarURL string `json:"avatar_url"`
}

type Service struct {
	cfg      config.Config
	accounts *accounts.Store
	client   *http.Client
	mu       sync.RWMutex
	flows    map[string]*Flow
}

func New(cfg config.Config, store *accounts.Store) *Service {
	transport := &http.Transport{
		DisableKeepAlives: true,
		IdleConnTimeout:   30 * time.Second,
	}
	return &Service{
		cfg:      cfg,
		accounts: store,
		client:   &http.Client{Timeout: 15 * time.Second, Transport: transport},
		flows:    map[string]*Flow{},
	}
}

func (s *Service) Start(ctx context.Context) (Flow, error) {
	state := randomHex(32)
	flowID := randomHex(16)

	params := url.Values{
		"client_id":    {s.cfg.OAuthClientID},
		"redirect_uri": {s.cfg.OAuthRedirectURI},
		"response_type": {"code"},
		"scope":        {"openid profile email"},
		"state":        {state},
	}
	authorizeURL := s.cfg.OAuthAuthorizeURL + "?" + params.Encode()

	flow := Flow{
		FlowID:       flowID,
		AuthorizeURL: authorizeURL,
		State:        state,
		Status:       "pending",
	}

	s.mu.Lock()
	s.flows[flowID] = &flow
	s.mu.Unlock()

	return flow, nil
}

func (s *Service) Exchange(ctx context.Context, flowID, code, state string) (map[string]any, error) {
	s.mu.Lock()
	flow := s.flows[flowID]
	if flow != nil {
		delete(s.flows, flowID)
	}
	s.mu.Unlock()

	if flow == nil {
		return nil, errors.New("unknown OAuth flow; start login again")
	}
	if flow.State != "" && flow.State != state {
		return nil, errors.New("OAuth state mismatch; possible CSRF")
	}

	var token tokenResponse
	if err := s.tokenRequest(ctx, code, &token); err != nil {
		return nil, err
	}

	userInfo, err := s.userInfoRequest(ctx, token.Data.Token)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch user info: %w", err)
	}

	user := accounts.User{
		ID:     userInfo.ID,
		UserID: userInfo.UserID,
		Email:  userInfo.Email,
		Name:   userInfo.Name,
		Avatar: first(userInfo.Avatar, userInfo.AvatarURL),
	}
	if user.ID == "" {
		user.ID = userInfo.UserID
	}
	if user.Name == "" {
		user.Name = userInfo.Nickname
	}

	account, err := s.accounts.Upsert(user, token.Data.Token, token.Data.AccessToken)
	if err != nil {
		return nil, err
	}

	return map[string]any{
		"status":  "ready",
		"account": account,
	}, nil
}

func (s *Service) Status() []Flow {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]Flow, 0, len(s.flows))
	for _, flow := range s.flows {
		result = append(result, *flow)
	}
	return result
}

func (s *Service) tokenRequest(ctx context.Context, code string, target *tokenResponse) error {
	body := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {s.cfg.OAuthRedirectURI},
		"client_id":    {s.cfg.OAuthClientID},
	}
	encoded := body.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.OAuthTokenURL, bytes.NewReader([]byte(encoded)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	response, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	rawBody, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("OAuth token request failed reading response: %w", err)
	}

	var status struct {
		Code    int    `json:"code"`
		Msg     string `json:"msg"`
		Message string `json:"message"`
	}
	if len(bytes.TrimSpace(rawBody)) > 0 {
		_ = json.Unmarshal(rawBody, &status)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || status.Code != 0 {
		message := first(status.Msg, status.Message, strings.TrimSpace(string(rawBody)), response.Status)
		return fmt.Errorf("OAuth token exchange failed: HTTP %d %s", response.StatusCode, message)
	}
	if err := json.Unmarshal(rawBody, target); err != nil {
		return fmt.Errorf("OAuth token response is invalid JSON: %w", err)
	}
	if target.Data.Token == "" {
		return errors.New("OAuth token response did not include a token")
	}
	return nil
}

func (s *Service) userInfoRequest(ctx context.Context, token string) (userInfoResponse, error) {
	var result userInfoResponse
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cfg.OAuthUserInfoURL, nil)
	if err != nil {
		return result, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	response, err := s.client.Do(req)
	if err != nil {
		return result, err
	}
	defer response.Body.Close()

	rawBody, err := io.ReadAll(response.Body)
	if err != nil {
		return result, fmt.Errorf("OAuth userinfo request failed reading response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return result, fmt.Errorf("OAuth userinfo request failed: HTTP %d %s", response.StatusCode, strings.TrimSpace(string(rawBody)))
	}
	if err := json.Unmarshal(rawBody, &result); err != nil {
		return result, fmt.Errorf("OAuth userinfo response is invalid JSON: %w", err)
	}
	return result, nil
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
