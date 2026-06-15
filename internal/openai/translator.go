package openai

import (
	"encoding/json"
	"fmt"
	"strings"

	"glm5.2proxy/internal/models"
	"glm5.2proxy/internal/state"
)

func ToAnthropic(body map[string]any, template map[string]any, model models.Model, thinking state.ThinkingSettings, defaultMaxTokens int) map[string]any {
	out := cloneMap(template)
	if out == nil {
		out = map[string]any{}
	}
	out["model"] = model.UpstreamID
	out["max_tokens"] = numberOr(body["max_tokens"], numberOr(body["max_completion_tokens"], numberOr(out["max_tokens"], float64(defaultMaxTokens))))
	out["stream"] = true
	system, messages := convertMessages(array(body["messages"]), template != nil)
	out["messages"] = messages
	if len(system) > 0 {
		out["system"] = system
	} else {
		delete(out, "system")
	}
	tools := convertTools(array(body["tools"]))
	if len(tools) > 0 {
		out["tools"] = tools
		out["tool_choice"] = convertToolChoice(body["tool_choice"])
	} else {
		delete(out, "tools")
		delete(out, "tool_choice")
	}
	if thinking.Enabled {
		out["thinking"] = map[string]any{"type": "enabled", "budget_tokens": thinking.BudgetTokens}
		out["output_config"] = map[string]any{"effort": thinking.Effort}
	} else {
		delete(out, "thinking")
		delete(out, "output_config")
	}
	for _, key := range []string{"temperature", "top_p"} {
		if _, ok := body[key].(float64); ok {
			out[key] = body[key]
		}
	}
	switch stop := body["stop"].(type) {
	case string:
		out["stop_sequences"] = []string{stop}
	case []any:
		out["stop_sequences"] = stop
	}
	return out
}

func convertMessages(input []any, forceBlocks bool) ([]any, []any) {
	system := []any{}
	messages := []any{}
	for _, raw := range input {
		message := object(raw)
		role := text(message["role"])
		if role == "system" || role == "developer" {
			if value := contentText(message["content"]); value != "" {
				system = append(system, map[string]any{"type": "text", "text": value})
			}
			continue
		}
		if role == "tool" {
			value := fmt.Sprintf("Tool result%s:\n%s", toolID(message), contentText(message["content"]))
			messages = append(messages, map[string]any{"role": "user", "content": anthropicContent(value, forceBlocks)})
			continue
		}
		targetRole := "user"
		if role == "assistant" {
			targetRole = "assistant"
		}
		content := anthropicContent(message["content"], forceBlocks)
		if calls := array(message["tool_calls"]); len(calls) > 0 {
			blocks := []any{}
			if value := contentText(message["content"]); value != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": value})
			}
			for _, rawCall := range calls {
				call := object(rawCall)
				function := object(call["function"])
				if text(call["type"]) != "function" {
					continue
				}
				var input any = map[string]any{}
				if json.Unmarshal([]byte(text(function["arguments"])), &input) != nil {
					input = map[string]any{"_raw": text(function["arguments"])}
				}
				blocks = append(blocks, map[string]any{"type": "tool_use", "id": first(text(call["id"]), randomID()), "name": first(text(function["name"]), "tool"), "input": input})
			}
			content = blocks
		}
		messages = append(messages, map[string]any{"role": targetRole, "content": content})
	}
	if len(messages) == 0 {
		messages = append(messages, map[string]any{"role": "user", "content": ""})
	}
	return system, messages
}

func anthropicContent(value any, forceBlocks bool) any {
	if textValue, ok := value.(string); ok {
		if forceBlocks {
			return []any{map[string]any{"type": "text", "text": textValue}}
		}
		return textValue
	}
	parts := array(value)
	blocks := []any{}
	for _, raw := range parts {
		if value, ok := raw.(string); ok {
			blocks = append(blocks, map[string]any{"type": "text", "text": value})
			continue
		}
		part := object(raw)
		switch text(part["type"]) {
		case "text", "input_text":
			blocks = append(blocks, map[string]any{"type": "text", "text": text(part["text"])})
		case "image_url":
			image := object(part["image_url"])
			url := text(image["url"])
			if strings.HasPrefix(url, "data:") {
				if separator := strings.Index(url, ";base64,"); separator > 5 {
					blocks = append(blocks, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": url[5:separator], "data": url[separator+8:]}})
				}
			}
		}
	}
	if !forceBlocks && len(blocks) == 1 && text(object(blocks[0])["type"]) == "text" {
		return text(object(blocks[0])["text"])
	}
	return blocks
}

func convertTools(input []any) []any {
	result := []any{}
	for _, raw := range input {
		tool := object(raw)
		function := object(tool["function"])
		if text(tool["type"]) != "function" || text(function["name"]) == "" {
			continue
		}
		schema := function["parameters"]
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		result = append(result, map[string]any{"name": text(function["name"]), "description": text(function["description"]), "input_schema": schema})
	}
	return result
}

func convertToolChoice(value any) any {
	if value == nil || value == "auto" {
		return map[string]any{"type": "auto"}
	}
	if value == "none" {
		return map[string]any{"type": "none"}
	}
	if value == "required" {
		return map[string]any{"type": "any"}
	}
	choice := object(value)
	function := object(choice["function"])
	if text(choice["type"]) == "function" && text(function["name"]) != "" {
		return map[string]any{"type": "tool", "name": text(function["name"])}
	}
	return map[string]any{"type": "auto"}
}

func contentText(value any) string {
	if textValue, ok := value.(string); ok {
		return textValue
	}
	var values []string
	for _, raw := range array(value) {
		if textValue, ok := raw.(string); ok {
			values = append(values, textValue)
			continue
		}
		part := object(raw)
		if value := text(part["text"]); value != "" {
			values = append(values, value)
		}
	}
	return strings.Join(values, "\n")
}

func toolID(message map[string]any) string {
	if id := text(message["tool_call_id"]); id != "" {
		return " (" + id + ")"
	}
	return ""
}

func cloneMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	raw, _ := json.Marshal(value)
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	return result
}

func numberOr(value any, fallback float64) float64 {
	if number, ok := value.(float64); ok {
		return number
	}
	return fallback
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
