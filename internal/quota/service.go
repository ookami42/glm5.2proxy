package quota

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"glm5.2proxy/internal/config"
	"glm5.2proxy/internal/models"
	"glm5.2proxy/internal/upstream"
)

type Balance struct {
	ID           string     `json:"id"`
	Model        string     `json:"model"`
	Meter        string     `json:"meter,omitempty"`
	UnitType     string     `json:"unitType,omitempty"`
	Total        *int64     `json:"total"`
	Used         *int64     `json:"used"`
	Reserved     *int64     `json:"reserved"`
	Remaining    *int64     `json:"remaining"`
	Available    *int64     `json:"available"`
	UsagePercent *float64   `json:"usagePercent"`
	PeriodStart  *time.Time `json:"periodStart"`
	PeriodEnd    *time.Time `json:"periodEnd"`
	ExpiresAt    *time.Time `json:"expiresAt"`
}

type Entitlement struct {
	ID       string `json:"id"`
	Model    string `json:"model"`
	Meter    string `json:"meter,omitempty"`
	UnitType string `json:"unitType,omitempty"`
	Granted  *int64 `json:"granted"`
	Period   string `json:"period,omitempty"`
}

type Plan struct {
	ID           string        `json:"id"`
	Name         string        `json:"name"`
	Status       string        `json:"status"`
	StartsAt     *time.Time    `json:"startsAt"`
	EndsAt       *time.Time    `json:"endsAt"`
	Entitlements []Entitlement `json:"entitlements"`
}

type Snapshot struct {
	Object      string     `json:"object"`
	GeneratedAt time.Time  `json:"generatedAt"`
	ServerTime  *time.Time `json:"serverTime"`
	Plans       []Plan     `json:"plans"`
	Balances    []Balance  `json:"balances"`
}

type Service struct {
	cfg    config.Config
	client *http.Client
}

func New(cfg config.Config) *Service {
	return &Service{cfg: cfg, client: &http.Client{Timeout: 15 * time.Second}}
}

func (s *Service) Snapshot(ctx context.Context, upstreamConfig upstream.Config) (Snapshot, error) {
	type result struct {
		kind string
		body map[string]any
		err  error
	}
	channel := make(chan result, 2)
	for _, kind := range []string{"current", "balance"} {
		go func(kind string) {
			body, err := s.fetch(ctx, upstreamConfig, kind)
			channel <- result{kind: kind, body: body, err: err}
		}(kind)
	}
	var current, balance map[string]any
	for range 2 {
		item := <-channel
		if item.err != nil {
			return Snapshot{}, item.err
		}
		if item.kind == "current" {
			current = item.body
		} else {
			balance = item.body
		}
	}
	return normalizeSnapshot(current, balance), nil
}

func (s *Service) ModelBalance(ctx context.Context, upstreamConfig upstream.Config, model models.Model) (*Balance, error) {
	if !upstreamConfig.HasAuthorization {
		return nil, nil
	}
	body, err := s.fetch(ctx, upstreamConfig, "balance")
	if err != nil {
		return nil, err
	}
	data := object(body["data"])
	for _, item := range array(data["balances"]) {
		value := object(item)
		if strings.EqualFold(text(value["show_name"]), model.UpstreamID) {
			balance := normalizeBalance(value)
			return &balance, nil
		}
	}
	return nil, nil
}

func (s *Service) fetch(ctx context.Context, upstreamConfig upstream.Config, kind string) (map[string]any, error) {
	appVersion := upstreamConfig.BaseHeaders["x-zcode-app-version"]
	if appVersion == "" {
		appVersion = s.cfg.AppVersion
	}
	endpoint := strings.TrimRight(s.cfg.BillingBaseURL, "/") + "/" + kind + "?app_version=" + url.QueryEscape(appVersion)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", upstreamConfig.BaseHeaders["authorization"])
	request.Header.Set("User-Agent", first(upstreamConfig.BaseHeaders["user-agent"], "ZCode/"+appVersion))
	request.Header.Set("X-ZCode-App-Version", appVersion)
	response, err := s.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	var body map[string]any
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || integer(body["code"]) != 0 {
		return nil, fmt.Errorf("billing %s failed: HTTP %d %s", kind, response.StatusCode, first(text(body["msg"]), text(body["message"])))
	}
	return body, nil
}

func normalizeSnapshot(current, balances map[string]any) Snapshot {
	out := Snapshot{Object: "zcode.quota", GeneratedAt: time.Now().UTC(), Plans: []Plan{}, Balances: []Balance{}}
	currentData := object(current["data"])
	for _, item := range array(currentData["plans"]) {
		value := object(item)
		plan := Plan{ID: text(value["plan_id"]), Name: text(value["name"]), Status: text(value["status"]), StartsAt: unixTime(value["starts_at"]), EndsAt: unixTime(value["ends_at"]), Entitlements: []Entitlement{}}
		for _, raw := range array(value["entitlements"]) {
			entitlement := object(raw)
			plan.Entitlements = append(plan.Entitlements, Entitlement{ID: text(entitlement["entitlement_id"]), Model: text(entitlement["show_name"]), Meter: text(entitlement["meter"]), UnitType: text(entitlement["unit_type"]), Granted: intPointer(entitlement["grant_units"]), Period: text(entitlement["period"])})
		}
		out.Plans = append(out.Plans, plan)
	}
	balanceData := object(balances["data"])
	out.ServerTime = unixTime(balanceData["server_time"])
	for _, item := range array(balanceData["balances"]) {
		out.Balances = append(out.Balances, normalizeBalance(object(item)))
	}
	return out
}

func normalizeBalance(value map[string]any) Balance {
	total := intPointer(value["total_units"])
	used := intPointer(value["used_units"])
	var percent *float64
	if total != nil && used != nil && *total > 0 {
		calculated := float64(*used) * 100 / float64(*total)
		percent = &calculated
	}
	return Balance{ID: text(value["entitlement_id"]), Model: text(value["show_name"]), Meter: text(value["meter"]), UnitType: text(value["unit_type"]), Total: total, Used: used, Reserved: intPointer(value["reserved_units"]), Remaining: intPointer(value["remaining_units"]), Available: intPointer(value["available_units"]), UsagePercent: percent, PeriodStart: unixTime(value["period_start"]), PeriodEnd: unixTime(value["period_end"]), ExpiresAt: unixTime(value["expires_at"])}
}

func unixTime(value any) *time.Time {
	seconds := integer(value)
	if seconds <= 0 {
		return nil
	}
	result := time.Unix(seconds, 0).UTC()
	return &result
}

func intPointer(value any) *int64 {
	if value == nil {
		return nil
	}
	result := integer(value)
	return &result
}

func integer(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case int:
		return int64(typed)
	case int64:
		return typed
	case json.Number:
		result, _ := typed.Int64()
		return result
	case string:
		result, _ := strconv.ParseInt(typed, 10, 64)
		return result
	default:
		return 0
	}
}

func object(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

func array(value any) []any {
	result, _ := value.([]any)
	return result
}

func text(value any) string {
	result, _ := value.(string)
	return result
}

func first(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
