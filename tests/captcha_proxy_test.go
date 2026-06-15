package tests

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"glm5.2proxy/internal/captcha"
	"glm5.2proxy/internal/proxy"
	"glm5.2proxy/internal/upstream"
)

func TestCaptchaBridgeAndProxyRetry(t *testing.T) {
	cfg := testConfig(t)
	cfg.CaptchaEnabled = true
	bridge := captcha.NewBridge(cfg)
	pollRecorder := httptest.NewRecorder()
	pollRequest := httptest.NewRequest(http.MethodGet, "/zcode/captcha/poll?client=headless-browser", nil)
	pollDone := make(chan struct{})
	go func() {
		bridge.Poll(pollRecorder, pollRequest)
		close(pollDone)
	}()
	time.Sleep(20 * time.Millisecond)
	tokenResult := make(chan string, 1)
	go func() {
		token, _ := bridge.Fresh(context.Background())
		tokenResult <- token
	}()
	<-pollDone
	var captchaRequest captcha.Request
	if err := json.Unmarshal(pollRecorder.Body.Bytes(), &captchaRequest); err != nil {
		t.Fatal(err)
	}
	submit := httptest.NewRequest(http.MethodPost, "/zcode/captcha/submit", strings.NewReader(`{"id":"`+captchaRequest.ID+`","token":"proof"}`))
	submit.Header.Set("Content-Type", "application/json")
	bridge.Submit(httptest.NewRecorder(), submit)
	if token := <-tokenResult; token != "proof" {
		t.Fatalf("unexpected captcha token: %q", token)
	}

	cfg.CaptchaEnabled = false
	var attempts atomic.Int32
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if attempts.Add(1) == 1 {
			w.Write([]byte("event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"code\":\"1305\",\"message\":\"busy\"}}\n\n"))
			return
		}
		w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer mock.Close()
	service := proxy.New(cfg, bridge)
	completion, _, err := service.Collect(context.Background(), upstream.Config{Endpoint: mock.URL, BaseHeaders: map[string]string{"authorization": "Bearer test"}, HasAuthorization: true}, map[string]any{"model": "GLM-5.2"})
	if err != nil || completion.Text != "ok" || attempts.Load() != 2 {
		t.Fatalf("retry failed: completion=%+v attempts=%d err=%v", completion, attempts.Load(), err)
	}
}

func TestProxyStreamingEvents(t *testing.T) {
	cfg := testConfig(t)
	cfg.CaptchaEnabled = false
	bridge := captcha.NewBridge(cfg)
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"stream\"}}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer mock.Close()
	service := proxy.New(cfg, bridge)
	var content string
	finishes := 0
	_, err := service.Stream(context.Background(), upstream.Config{Endpoint: mock.URL, BaseHeaders: map[string]string{"authorization": "Bearer test"}, HasAuthorization: true}, map[string]any{"model": "GLM-5.2"}, func(event proxy.StreamEvent) error {
		if value, ok := event.Delta["content"].(string); ok {
			content += value
		}
		if event.FinishReason != "" {
			finishes++
		}
		return nil
	})
	if err != nil || content != "stream" || finishes != 2 {
		t.Fatalf("unexpected raw stream events: content=%q finishes=%d err=%v", content, finishes, err)
	}
}
