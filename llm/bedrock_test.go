package llm

import (
	"encoding/json"
	"testing"
)

func TestClaudeRequestMarshal(t *testing.T) {
	req := claudeRequest{
		AnthropicVersion: "bedrock-2023-05-31",
		MaxTokens:        2000,
		Temperature:      0.3,
		System:           "You are a code reviewer.",
		Messages: []claudeMessage{
			{Role: "user", Content: "Review this code."},
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got["anthropic_version"] != "bedrock-2023-05-31" {
		t.Errorf("anthropic_version = %v, want bedrock-2023-05-31", got["anthropic_version"])
	}
	if got["system"] != "You are a code reviewer." {
		t.Errorf("system = %v, want 'You are a code reviewer.'", got["system"])
	}
	if int(got["max_tokens"].(float64)) != 2000 {
		t.Errorf("max_tokens = %v, want 2000", got["max_tokens"])
	}
}

func TestClaudeResponseUnmarshal(t *testing.T) {
	raw := `{
		"content": [{"type": "text", "text": "{\"status\":\"passed\",\"findings\":[]}"}],
		"model": "claude-sonnet-4-20250514",
		"stop_reason": "end_turn",
		"usage": {"input_tokens": 100, "output_tokens": 50}
	}`

	var resp claudeResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(resp.Content) != 1 {
		t.Fatalf("content length = %d, want 1", len(resp.Content))
	}
	if resp.Content[0].Type != "text" {
		t.Errorf("content type = %q, want text", resp.Content[0].Type)
	}
	if resp.Usage.InputTokens != 100 {
		t.Errorf("input_tokens = %d, want 100", resp.Usage.InputTokens)
	}
	if resp.Usage.OutputTokens != 50 {
		t.Errorf("output_tokens = %d, want 50", resp.Usage.OutputTokens)
	}
}

func TestClaudeResponseEmptyContent(t *testing.T) {
	raw := `{
		"content": [],
		"model": "claude-sonnet-4-20250514",
		"stop_reason": "end_turn",
		"usage": {"input_tokens": 10, "output_tokens": 0}
	}`

	var resp claudeResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(resp.Content) != 0 {
		t.Errorf("content length = %d, want 0", len(resp.Content))
	}
}

func TestClaudeRequestOmitsEmptySystem(t *testing.T) {
	req := claudeRequest{
		AnthropicVersion: "bedrock-2023-05-31",
		MaxTokens:        1000,
		Messages: []claudeMessage{
			{Role: "user", Content: "hello"},
		},
	}

	data, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if _, ok := got["system"]; ok {
		t.Error("expected system field to be omitted when empty")
	}
	if _, ok := got["temperature"]; ok {
		t.Error("expected temperature field to be omitted when zero")
	}
}
