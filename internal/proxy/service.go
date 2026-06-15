package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"glm5.2proxy/internal/captcha"
	"glm5.2proxy/internal/config"
	"glm5.2proxy/internal/upstream"
)

type ToolCall struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type Completion struct {
	Text         string
	FinishReason string
	ToolCalls    []ToolCall
}

type UpstreamError struct {
	Message   string `json:"message"`
	Type      string `json:"type"`
	Code      any    `json:"code,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	Status    int    `json:"status,omitempty"`
	Attempts  int    `json:"attempts,omitempty"`
}

func (e *UpstreamError) Error() string { return e.Message }

type Service struct {
	cfg     config.Config
	captcha *captcha.Bridge
	client  *http.Client
}

type StreamEvent struct {
	Delta        map[string]any
	FinishReason string
}

func New(cfg config.Config, bridge *captcha.Bridge) *Service {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 100
	transport.MaxIdleConnsPerHost = 20
	transport.IdleConnTimeout = 90 * time.Second
	return &Service{cfg: cfg, captcha: bridge, client: &http.Client{Transport: transport}}
}

func (s *Service) Request(ctx context.Context, upstreamConfig upstream.Config, body map[string]any) (*http.Response, int, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, 0, err
	}
	for attempt := 1; attempt <= s.cfg.RetryMaxAttempts; attempt++ {
		response, err := s.requestOnce(ctx, upstreamConfig, raw)
		if err != nil {
			if attempt < s.cfg.RetryMaxAttempts {
				s.wait(ctx, attempt, "fetch error")
				continue
			}
			return nil, attempt, err
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			upstreamError := decodeHTTPError(response)
			if overloaded(upstreamError) && attempt < s.cfg.RetryMaxAttempts {
				s.wait(ctx, attempt, "HTTP overloaded")
				continue
			}
			upstreamError.Attempts = attempt
			return nil, attempt, upstreamError
		}
		return response, attempt, nil
	}
	return nil, s.cfg.RetryMaxAttempts, &UpstreamError{Message: "ZCode upstream overloaded after retries", Type: "overloaded_error", Code: "1305"}
}

func (s *Service) Collect(ctx context.Context, upstreamConfig upstream.Config, body map[string]any) (Completion, int, error) {
	for attempt := 1; attempt <= s.cfg.RetryMaxAttempts; attempt++ {
		response, _, err := s.RequestSingleAttempt(ctx, upstreamConfig, body)
		if err != nil {
			if overloaded(err) && attempt < s.cfg.RetryMaxAttempts {
				s.wait(ctx, attempt, "upstream overloaded")
				continue
			}
			return Completion{}, attempt, err
		}
		completion, parseErr := collectSSE(response.Body)
		response.Body.Close()
		if parseErr != nil {
			if overloaded(parseErr) && attempt < s.cfg.RetryMaxAttempts && completion.Text == "" {
				s.wait(ctx, attempt, "SSE overloaded")
				continue
			}
			return completion, attempt, parseErr
		}
		return completion, attempt, nil
	}
	return Completion{}, s.cfg.RetryMaxAttempts, &UpstreamError{Message: "ZCode upstream overloaded after retries", Type: "overloaded_error", Code: "1305"}
}

func (s *Service) Stream(ctx context.Context, upstreamConfig upstream.Config, body map[string]any, emit func(StreamEvent) error) (int, error) {
	for attempt := 1; attempt <= s.cfg.RetryMaxAttempts; attempt++ {
		response, _, err := s.RequestSingleAttempt(ctx, upstreamConfig, body)
		if err != nil {
			if overloaded(err) && attempt < s.cfg.RetryMaxAttempts {
				s.wait(ctx, attempt, "upstream overloaded")
				continue
			}
			return attempt, err
		}
		emitted := false
		streamErr := readSSE(response.Body, func(event string, data []byte) error {
			if string(data) == "[DONE]" || len(data) == 0 {
				return nil
			}
			var message map[string]any
			if json.Unmarshal(data, &message) != nil {
				return nil
			}
			messageType := text(message["type"])
			if messageType == "error" || event == "error" {
				return normalizeError(message, 502)
			}
			if messageType == "content_block_start" {
				block := object(message["content_block"])
				if text(block["type"]) == "tool_use" {
					emitted = true
					return emit(StreamEvent{Delta: map[string]any{"tool_calls": []any{map[string]any{"index": integer(message["index"]), "id": text(block["id"]), "type": "function", "function": map[string]any{"name": text(block["name"]), "arguments": ""}}}}})
				}
			}
			if messageType == "content_block_delta" {
				delta := object(message["delta"])
				switch text(delta["type"]) {
				case "text_delta":
					if value := text(delta["text"]); value != "" {
						emitted = true
						return emit(StreamEvent{Delta: map[string]any{"content": value}})
					}
				case "input_json_delta":
					if value := text(delta["partial_json"]); value != "" {
						emitted = true
						return emit(StreamEvent{Delta: map[string]any{"tool_calls": []any{map[string]any{"index": integer(message["index"]), "function": map[string]any{"arguments": value}}}}})
					}
				}
			}
			if messageType == "message_delta" {
				if reason := text(object(message["delta"])["stop_reason"]); reason != "" {
					return emit(StreamEvent{Delta: map[string]any{}, FinishReason: mapFinishReason(reason)})
				}
			}
			if messageType == "message_stop" {
				return emit(StreamEvent{Delta: map[string]any{}, FinishReason: "stop"})
			}
			return nil
		})
		response.Body.Close()
		if streamErr != nil && overloaded(streamErr) && !emitted && attempt < s.cfg.RetryMaxAttempts {
			s.wait(ctx, attempt, "SSE overloaded")
			continue
		}
		return attempt, streamErr
	}
	return s.cfg.RetryMaxAttempts, &UpstreamError{Message: "ZCode upstream overloaded after retries", Type: "overloaded_error", Code: "1305"}
}

func (s *Service) RequestSingleAttempt(ctx context.Context, upstreamConfig upstream.Config, body map[string]any) (*http.Response, int, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, 1, err
	}
	response, err := s.requestOnce(ctx, upstreamConfig, raw)
	if err != nil {
		return nil, 1, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, 1, decodeHTTPError(response)
	}
	return response, 1, nil
}

func (s *Service) requestOnce(parent context.Context, upstreamConfig upstream.Config, raw []byte) (*http.Response, error) {
	ctx, cancel := context.WithTimeout(parent, s.cfg.UpstreamTimeout)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamConfig.Endpoint, bytes.NewReader(raw))
	if err != nil {
		cancel()
		return nil, err
	}
	for key, value := range upstreamConfig.BaseHeaders {
		request.Header.Set(key, value)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("X-Request-ID", randomID())
	request.Header.Set("X-ZCode-Trace-ID", randomID())
	request.Header.Set("X-Query-ID", randomID())
	if s.cfg.CaptchaEnabled {
		token, captchaErr := s.captcha.Fresh(ctx)
		if captchaErr != nil {
			cancel()
			return nil, captchaErr
		}
		request.Header.Set("X-Aliyun-Captcha-Verify-Param", token)
	}
	response, err := s.client.Do(request)
	if err != nil {
		cancel()
		return nil, err
	}
	response.Body = &cancelReadCloser{ReadCloser: response.Body, cancel: cancel}
	return response, nil
}

type cancelReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelReadCloser) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()
	return err
}

func collectSSE(reader io.Reader) (Completion, error) {
	result := Completion{FinishReason: "stop", ToolCalls: []ToolCall{}}
	toolCalls := map[int]*ToolCall{}
	err := readSSE(reader, func(event string, data []byte) error {
		if string(data) == "[DONE]" || len(data) == 0 {
			return nil
		}
		var message map[string]any
		if json.Unmarshal(data, &message) != nil {
			return nil
		}
		messageType := text(message["type"])
		if messageType == "error" || event == "error" {
			return normalizeError(message, 502)
		}
		if messageType == "content_block_start" {
			block := object(message["content_block"])
			if text(block["type"]) == "tool_use" {
				index := integer(message["index"])
				call := &ToolCall{ID: text(block["id"]), Type: "function"}
				call.Function.Name = text(block["name"])
				toolCalls[index] = call
			}
		}
		if messageType == "content_block_delta" {
			delta := object(message["delta"])
			switch text(delta["type"]) {
			case "text_delta":
				result.Text += text(delta["text"])
			case "input_json_delta":
				if call := toolCalls[integer(message["index"])]; call != nil {
					call.Function.Arguments += text(delta["partial_json"])
				}
			}
		}
		if messageType == "message_delta" {
			result.FinishReason = mapFinishReason(text(object(message["delta"])["stop_reason"]))
		}
		return nil
	})
	for index := 0; index < len(toolCalls); index++ {
		if call := toolCalls[index]; call != nil {
			result.ToolCalls = append(result.ToolCalls, *call)
		}
	}
	return result, err
}

func readSSE(reader io.Reader, callback func(string, []byte) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	event := "message"
	var data strings.Builder
	flush := func() error {
		if data.Len() == 0 {
			event = "message"
			return nil
		}
		value := strings.TrimSuffix(data.String(), "\n")
		err := callback(event, []byte(value))
		event = "message"
		data.Reset()
		return err
	}
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			if err := flush(); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			data.WriteByte('\n')
		}
	}
	if err := flush(); err != nil {
		return err
	}
	return scanner.Err()
}

func decodeHTTPError(response *http.Response) *UpstreamError {
	defer response.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	var payload map[string]any
	_ = json.Unmarshal(raw, &payload)
	return normalizeError(payload, response.StatusCode)
}

func normalizeError(payload map[string]any, status int) *UpstreamError {
	value := payload
	if nested := object(payload["error"]); nested != nil {
		value = nested
	}
	message := first(text(value["message"]), text(payload["msg"]), text(payload["message"]), "Unknown upstream error")
	return &UpstreamError{Message: message, Type: first(text(value["type"]), "zcode_upstream_error"), Code: firstAny(value["code"], payload["code"]), RequestID: first(text(payload["request_id"]), text(value["request_id"])), Status: status}
}

func overloaded(err error) bool {
	var upstreamError *UpstreamError
	if !errors.As(err, &upstreamError) {
		return false
	}
	return fmt.Sprint(upstreamError.Code) == "1305" || upstreamError.Type == "overloaded_error" || strings.Contains(upstreamError.Message, "[1305]")
}

func (s *Service) wait(ctx context.Context, attempt int, reason string) {
	delay := s.cfg.RetryBaseDelay * time.Duration(attempt)
	if delay > s.cfg.RetryMaxDelay {
		delay = s.cfg.RetryMaxDelay
	}
	log.Printf("upstream retry %d/%d after %s; waiting %s", attempt+1, s.cfg.RetryMaxAttempts, reason, delay)
	timer := time.NewTimer(delay)
	select {
	case <-timer.C:
	case <-ctx.Done():
		timer.Stop()
	}
}

func randomID() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func mapFinishReason(reason string) string {
	switch reason {
	case "", "end_turn", "stop_sequence", "stop":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use", "tool_calls":
		return "tool_calls"
	default:
		return reason
	}
}

func object(value any) map[string]any {
	result, _ := value.(map[string]any)
	return result
}

func text(value any) string {
	result, _ := value.(string)
	return result
}

func integer(value any) int {
	number, _ := value.(float64)
	return int(number)
}

func first(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstAny(values ...any) any {
	for _, value := range values {
		if value != nil && fmt.Sprint(value) != "" {
			return value
		}
	}
	return nil
}
