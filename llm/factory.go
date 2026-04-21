package llm

import (
	"fmt"

	"github.com/rikdc/temporal-code-reviewer/config"
	"go.uber.org/zap"
)

// NewReviewer creates the appropriate LLM client based on the configured provider.
func NewReviewer(cfg *config.Config, logger *zap.Logger) (Reviewer, error) {
	switch cfg.Provider {
	case "openrouter", "":
		return NewClient(&cfg.OpenRouter, logger), nil
	case "bedrock":
		return NewBedrockClient(&cfg.Bedrock, logger)
	default:
		return nil, fmt.Errorf("unsupported llm provider: %q", cfg.Provider)
	}
}
