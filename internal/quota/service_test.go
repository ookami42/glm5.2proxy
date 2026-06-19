package quota

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"glm5.2proxy/internal/config"
	"glm5.2proxy/internal/upstream"
)

func TestSnapshotReportsEmptyHTTPStatusInsteadOfEOF(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()

	cfg := config.Load()
	cfg.BillingBaseURL = server.URL
	service := New(cfg)

	_, err := service.Snapshot(context.Background(), upstream.Config{
		BaseHeaders: map[string]string{
			"authorization":       "Bearer test-token",
			"x-zcode-app-version": "3.0.1",
		},
		HasAuthorization: true,
	})
	if err == nil {
		t.Fatal("expected quota snapshot error")
	}
	message := err.Error()
	if !strings.Contains(message, "HTTP 429") || strings.Contains(message, "EOF") {
		t.Fatalf("unexpected quota error message: %q", message)
	}
}

func TestBeginRequestHonorsContextWhileGateIsBusy(t *testing.T) {
	service := New(config.Config{})
	release, err := service.beginRequest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err = service.beginRequest(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline while waiting for billing gate, got %v", err)
	}
}

func TestSnapshotCachedReusesFreshSnapshot(t *testing.T) {
	currentCalls := 0
	balanceCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/current":
			currentCalls++
			_, _ = w.Write([]byte(`{"code":0,"data":{"plans":[]}}`))
		case "/balance":
			balanceCalls++
			_, _ = w.Write([]byte(`{"code":0,"data":{"balances":[]}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cfg := config.Config{BillingBaseURL: server.URL, AppVersion: "3.0.1"}
	service := New(cfg)
	upstreamConfig := upstream.Config{
		BaseHeaders: map[string]string{
			"authorization":       "Bearer test-token",
			"x-zcode-app-version": "3.0.1",
		},
		HasAuthorization: true,
	}

	if _, err := service.SnapshotCached(context.Background(), upstreamConfig, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SnapshotCached(context.Background(), upstreamConfig, time.Minute); err != nil {
		t.Fatal(err)
	}
	if currentCalls != 1 || balanceCalls != 1 {
		t.Fatalf("expected cached quota snapshot, current=%d balance=%d", currentCalls, balanceCalls)
	}
}
