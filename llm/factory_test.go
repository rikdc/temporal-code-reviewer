package llm

import (
	"strings"
	"testing"

	"github.com/rikdc/temporal-code-reviewer/config"
	"go.uber.org/zap"
)

func TestNewReviewer(t *testing.T) {
	logger := zap.NewNop()

	t.Run("openrouter explicit", func(t *testing.T) {
		cfg := &config.Config{
			Provider: "openrouter",
			OpenRouter: config.OpenRouterConfig{
				APIKey:  "test-key",
				BaseURL: "https://openrouter.ai/api/v1",
				Timeout: 30,
			},
		}
		r, err := NewReviewer(cfg, logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := r.(*Client); !ok {
			t.Errorf("expected *Client, got %T", r)
		}
	})

	t.Run("empty provider defaults to openrouter", func(t *testing.T) {
		cfg := &config.Config{
			Provider: "",
			OpenRouter: config.OpenRouterConfig{
				APIKey:  "test-key",
				BaseURL: "https://openrouter.ai/api/v1",
				Timeout: 30,
			},
		}
		r, err := NewReviewer(cfg, logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := r.(*Client); !ok {
			t.Errorf("expected *Client, got %T", r)
		}
	})

	t.Run("bedrock provider", func(t *testing.T) {
		cfg := &config.Config{
			Provider: "bedrock",
			Bedrock: config.BedrockConfig{
				Region:  "us-east-1",
				Timeout: 120,
			},
		}
		r, err := NewReviewer(cfg, logger)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if _, ok := r.(*BedrockClient); !ok {
			t.Errorf("expected *BedrockClient, got %T", r)
		}
	})

	t.Run("unknown provider", func(t *testing.T) {
		cfg := &config.Config{
			Provider: "unknown",
		}
		_, err := NewReviewer(cfg, logger)
		if err == nil {
			t.Fatal("expected error for unknown provider")
		}
		if !strings.Contains(err.Error(), "unsupported") {
			t.Errorf("error = %q, want to contain 'unsupported'", err.Error())
		}
	})
}
