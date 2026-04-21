package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/rikdc/temporal-code-reviewer/config"
	"go.uber.org/zap"
)

// BedrockClient provides AWS Bedrock LLM integration using Claude's native API.
type BedrockClient struct {
	client  *bedrockruntime.Client
	timeout time.Duration
	logger  *zap.Logger
}

// NewBedrockClient creates a new Bedrock client using the standard AWS credential chain.
func NewBedrockClient(cfg *config.BedrockConfig, logger *zap.Logger) (*BedrockClient, error) {
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(cfg.Region),
	)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	return &BedrockClient{
		client:  bedrockruntime.NewFromConfig(awsCfg),
		timeout: time.Duration(cfg.Timeout) * time.Second,
		logger:  logger,
	}, nil
}

// claudeRequest is the native Claude API request format for Bedrock.
type claudeRequest struct {
	AnthropicVersion string          `json:"anthropic_version"`
	MaxTokens        int             `json:"max_tokens"`
	Temperature      float64         `json:"temperature,omitempty"`
	System           string          `json:"system,omitempty"`
	Messages         []claudeMessage `json:"messages"`
}

type claudeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// claudeResponse is the native Claude API response format from Bedrock.
type claudeResponse struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Model      string `json:"model"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// Review sends a review request to Bedrock and returns structured output.
func (c *BedrockClient) Review(ctx context.Context, req ReviewRequest) (*ReviewResponse, error) {
	start := time.Now()
	c.logger.Info("Sending LLM request",
		zap.String("provider", "bedrock"),
		zap.String("agent", req.AgentName),
		zap.String("model", req.Model),
		zap.Int("max_tokens", req.MaxTokens),
		zap.Float64("temperature", req.Temperature))

	body, err := json.Marshal(claudeRequest{
		AnthropicVersion: "bedrock-2023-05-31",
		MaxTokens:        req.MaxTokens,
		Temperature:      req.Temperature,
		System:           req.SystemPrompt,
		Messages: []claudeMessage{
			{Role: "user", Content: req.UserPrompt},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("marshal bedrock request: %w", err)
	}

	output, err := c.client.InvokeModel(ctx, &bedrockruntime.InvokeModelInput{
		ModelId:     aws.String(req.Model),
		ContentType: aws.String("application/json"),
		Accept:      aws.String("application/json"),
		Body:        body,
	})
	if err != nil {
		c.logger.Error("LLM request failed",
			zap.String("provider", "bedrock"),
			zap.String("agent", req.AgentName),
			zap.String("model", req.Model),
			zap.Error(err))
		return nil, fmt.Errorf("bedrock invoke for %s: %w", req.AgentName, err)
	}

	var resp claudeResponse
	if err := json.Unmarshal(output.Body, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal bedrock response: %w", err)
	}

	if len(resp.Content) == 0 {
		return nil, fmt.Errorf("no content in bedrock response")
	}

	duration := time.Since(start)
	c.logger.Info("LLM request completed",
		zap.String("provider", "bedrock"),
		zap.String("agent", req.AgentName),
		zap.String("model", req.Model),
		zap.Duration("latency", duration),
		zap.Int("input_tokens", resp.Usage.InputTokens),
		zap.Int("output_tokens", resp.Usage.OutputTokens),
		zap.Int("total_tokens", resp.Usage.InputTokens+resp.Usage.OutputTokens))

	return &ReviewResponse{
		Content:      resp.Content[0].Text,
		Model:        req.Model,
		InputTokens:  resp.Usage.InputTokens,
		OutputTokens: resp.Usage.OutputTokens,
	}, nil
}
