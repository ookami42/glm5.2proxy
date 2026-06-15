package tests

import (
	"testing"

	"glm5.2proxy/internal/models"
	"glm5.2proxy/internal/openai"
	"glm5.2proxy/internal/state"
)

func TestModelsAndOpenAITranslation(t *testing.T) {
	turbo, ok := models.Resolve("glm-5turbo")
	if !ok || turbo.UpstreamID != "GLM-5-Turbo" || turbo.DailyTokenAllowance != 2_000_000 {
		t.Fatalf("unexpected model resolution: %+v", turbo)
	}
	body := map[string]any{
		"model": "glm-5-turbo",
		"messages": []any{
			map[string]any{"role": "system", "content": "system"},
			map[string]any{"role": "user", "content": "hello"},
		},
		"tools": []any{map[string]any{"type": "function", "function": map[string]any{"name": "lookup", "description": "test", "parameters": map[string]any{"type": "object"}}}},
	}
	translated := openai.ToAnthropic(body, nil, turbo, state.ThinkingSettings{Enabled: true, BudgetTokens: 32000, Effort: "max"}, 64000)
	if translated["model"] != "GLM-5-Turbo" {
		t.Fatalf("wrong upstream model: %+v", translated)
	}
	thinking := translated["thinking"].(map[string]any)
	if thinking["budget_tokens"] != 32000 || translated["output_config"].(map[string]any)["effort"] != "max" {
		t.Fatalf("thinking settings missing: %+v", translated)
	}
	if len(translated["tools"].([]any)) != 1 || len(translated["messages"].([]any)) != 1 {
		t.Fatalf("message/tool translation failed: %+v", translated)
	}
}
