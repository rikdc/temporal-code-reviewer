package llm

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/rikdc/temporal-code-reviewer/config"
	"github.com/sashabaranov/go-openai"
	"go.uber.org/zap"
)

// Client provides OpenRouter LLM integration with structured JSON output
type Client struct {
	client *openai.Client
	config *config.OpenRouterConfig
	logger *zap.Logger
}

// NewClient creates a new LLM client configured for OpenRouter
func NewClient(cfg *config.OpenRouterConfig, logger *zap.Logger) *Client {
	clientConfig := openai.DefaultConfig(cfg.APIKey)
	clientConfig.BaseURL = cfg.BaseURL
	clientConfig.HTTPClient = &http.Client{
		Timeout: time.Duration(cfg.Timeout) * time.Second,
	}

	return &Client{
		client: openai.NewClientWithConfig(clientConfig),
		config: cfg,
		logger: logger,
	}
}

// ReviewRequest contains the parameters for an LLM review request
type ReviewRequest struct {
	AgentName    string
	Model        string
	SystemPrompt string
	UserPrompt   string
	MaxTokens    int
	Temperature  float64
}

// ReviewResponse contains the LLM response and usage metrics
type ReviewResponse struct {
	Content      string
	Model        string
	InputTokens  int
	OutputTokens int
}

// Review sends a review request to the LLM and returns structured JSON output
func (c *Client) Review(ctx context.Context, req ReviewRequest) (*ReviewResponse, error) {
	start := time.Now()
	c.logger.Info("Sending LLM request",
		zap.String("agent", req.AgentName),
		zap.String("model", req.Model),
		zap.Int("max_tokens", req.MaxTokens),
		zap.Float64("temperature", req.Temperature))

	resp, err := c.client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: req.Model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleSystem, Content: req.SystemPrompt},
			{Role: openai.ChatMessageRoleUser, Content: req.UserPrompt},
		},
		MaxTokens:   req.MaxTokens,
		Temperature: float32(req.Temperature),
		ResponseFormat: &openai.ChatCompletionResponseFormat{
			Type: openai.ChatCompletionResponseFormatTypeJSONObject,
		},
	})

	if err != nil {
		c.logger.Error("LLM request failed",
			zap.String("agent", req.AgentName),
			zap.String("model", req.Model),
			zap.Error(err))
		return nil, fmt.Errorf("llm request for %s: %w", req.AgentName, err)
	}

	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("no response choices from LLM")
	}

	duration := time.Since(start)
	inputTokens := resp.Usage.PromptTokens
	outputTokens := resp.Usage.CompletionTokens

	c.logger.Info("LLM request completed",
		zap.String("agent", req.AgentName),
		zap.String("model", resp.Model),
		zap.Duration("latency", duration),
		zap.Int("input_tokens", inputTokens),
		zap.Int("output_tokens", outputTokens),
		zap.Int("total_tokens", resp.Usage.TotalTokens))

	return &ReviewResponse{
		Content:      resp.Choices[0].Message.Content,
		Model:        resp.Model,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
	}, nil
}
