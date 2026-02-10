package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rikdc/temporal-code-reviewer/config"
	"github.com/sashabaranov/go-openai"
	"go.uber.org/zap/zaptest"
)

func TestNewClient(t *testing.T) {
	logger := zaptest.NewLogger(t)
	cfg := &config.OpenRouterConfig{
		APIKey:  "test-key",
		BaseURL: "https://openrouter.ai/api/v1",
		Timeout: 30,
	}

	client := NewClient(cfg, logger)
	if client == nil {
		t.Fatal("NewClient() returned nil")
	}
	if client.config == nil {
		t.Error("NewClient() config is nil")
	}
	if client.logger == nil {
		t.Error("NewClient() logger is nil")
	}
	if client.client == nil {
		t.Error("NewClient() openai client is nil")
	}
	if client.config.APIKey != "test-key" {
		t.Errorf("NewClient() APIKey = %q, want %q", client.config.APIKey, "test-key")
	}
}

func TestClient_Review_Success(t *testing.T) {
	// Create mock HTTP server
	mockResponse := openai.ChatCompletionResponse{
		ID:      "test-id",
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   "anthropic/claude-3.5-sonnet",
		Choices: []openai.ChatCompletionChoice{
			{
				Index: 0,
				Message: openai.ChatCompletionMessage{
					Role: openai.ChatMessageRoleAssistant,
					Content: `{
						"status": "passed",
						"findings": [],
						"summary": "All checks passed"
					}`,
				},
				FinishReason: openai.FinishReasonStop,
			},
		},
		Usage: openai.Usage{
			PromptTokens:     100,
			CompletionTokens: 50,
			TotalTokens:      150,
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.Method != http.MethodPost {
			t.Errorf("Expected POST request, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/chat/completions") {
			t.Errorf("Expected /chat/completions path, got %s", r.URL.Path)
		}

		// Verify Authorization header
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			t.Errorf("Expected Bearer token in Authorization header, got %s", auth)
		}

		// Return mock response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(mockResponse)
	}))
	defer server.Close()

	// Create client with test server
	logger := zaptest.NewLogger(t)
	cfg := &config.OpenRouterConfig{
		APIKey:  "test-key",
		BaseURL: server.URL,
		Timeout: 30,
	}

	openaiConfig := openai.DefaultConfig(cfg.APIKey)
	openaiConfig.BaseURL = cfg.BaseURL
	openaiClient := openai.NewClientWithConfig(openaiConfig)

	client := &Client{
		config: cfg,
		logger: logger,
		client: openaiClient,
	}

	// Test Review method
	ctx := context.Background()
	req := ReviewRequest{
		AgentName:    "Security",
		Model:        "anthropic/claude-3.5-sonnet",
		SystemPrompt: "You are a security reviewer",
		UserPrompt:   "Review this diff",
		MaxTokens:    2000,
		Temperature:  0.3,
	}

	resp, err := client.Review(ctx, req)
	if err != nil {
		t.Fatalf("Review() error = %v, want nil", err)
	}

	if resp == nil {
		t.Fatal("Review() returned nil response")
	}
	if resp.Content == "" {
		t.Error("Review() Content is empty")
	}
	if resp.InputTokens != 100 {
		t.Errorf("Review() InputTokens = %d, want 100", resp.InputTokens)
	}
	if resp.OutputTokens != 50 {
		t.Errorf("Review() OutputTokens = %d, want 50", resp.OutputTokens)
	}
	// Latency is logged but not returned in response
}

func TestClient_Review_JSONValidation(t *testing.T) {
	// Create mock server with invalid JSON response
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mockResponse := openai.ChatCompletionResponse{
			ID:      "test-id",
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   "anthropic/claude-3.5-sonnet",
			Choices: []openai.ChatCompletionChoice{
				{
					Index: 0,
					Message: openai.ChatCompletionMessage{
						Role:    openai.ChatMessageRoleAssistant,
						Content: "This is not valid JSON",
					},
					FinishReason: openai.FinishReasonStop,
				},
			},
			Usage: openai.Usage{
				PromptTokens:     100,
				CompletionTokens: 50,
				TotalTokens:      150,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(mockResponse)
	}))
	defer server.Close()

	logger := zaptest.NewLogger(t)
	cfg := &config.OpenRouterConfig{
		APIKey:  "test-key",
		BaseURL: server.URL,
		Timeout: 30,
	}

	openaiConfig := openai.DefaultConfig(cfg.APIKey)
	openaiConfig.BaseURL = cfg.BaseURL
	openaiClient := openai.NewClientWithConfig(openaiConfig)

	client := &Client{
		config: cfg,
		logger: logger,
		client: openaiClient,
	}

	ctx := context.Background()
	req := ReviewRequest{
		AgentName:    "Security",
		Model:        "anthropic/claude-3.5-sonnet",
		SystemPrompt: "You are a security reviewer",
		UserPrompt:   "Review this diff",
		MaxTokens:    2000,
		Temperature:  0.3,
	}

	// Should still succeed but with non-JSON content
	resp, err := client.Review(ctx, req)
	if err != nil {
		t.Fatalf("Review() error = %v, want nil (should handle non-JSON)", err)
	}

	if resp.Content == "" {
		t.Error("Review() Content is empty")
	}
	if resp.Content != "This is not valid JSON" {
		t.Errorf("Review() Content = %q, want 'This is not valid JSON'", resp.Content)
	}
}

func TestClient_Review_APIError(t *testing.T) {
	// Create mock server that returns error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"error": map[string]interface{}{
				"message": "Invalid API key",
				"type":    "invalid_request_error",
			},
		})
	}))
	defer server.Close()

	logger := zaptest.NewLogger(t)
	cfg := &config.OpenRouterConfig{
		APIKey:  "invalid-key",
		BaseURL: server.URL,
		Timeout: 30,
	}

	openaiConfig := openai.DefaultConfig(cfg.APIKey)
	openaiConfig.BaseURL = cfg.BaseURL
	openaiClient := openai.NewClientWithConfig(openaiConfig)

	client := &Client{
		config: cfg,
		logger: logger,
		client: openaiClient,
	}

	ctx := context.Background()
	req := ReviewRequest{
		AgentName:    "Security",
		Model:        "anthropic/claude-3.5-sonnet",
		SystemPrompt: "You are a security reviewer",
		UserPrompt:   "Review this diff",
		MaxTokens:    2000,
		Temperature:  0.3,
	}

	_, err := client.Review(ctx, req)
	if err == nil {
		t.Error("Review() with invalid API key should return error")
	}
}

func TestClient_Review_Timeout(t *testing.T) {
	// Create mock server with delay
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	logger := zaptest.NewLogger(t)
	cfg := &config.OpenRouterConfig{
		APIKey:  "test-key",
		BaseURL: server.URL,
		Timeout: 1, // 1 second timeout
	}

	openaiConfig := openai.DefaultConfig(cfg.APIKey)
	openaiConfig.BaseURL = cfg.BaseURL
	httpClient := &http.Client{
		Timeout: time.Duration(cfg.Timeout) * time.Second,
	}
	openaiConfig.HTTPClient = httpClient
	openaiClient := openai.NewClientWithConfig(openaiConfig)

	client := &Client{
		config: cfg,
		logger: logger,
		client: openaiClient,
	}

	// Create context with very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	req := ReviewRequest{
		AgentName:    "Security",
		Model:        "anthropic/claude-3.5-sonnet",
		SystemPrompt: "You are a security reviewer",
		UserPrompt:   "Review this diff",
		MaxTokens:    2000,
		Temperature:  0.3,
	}

	_, err := client.Review(ctx, req)
	if err == nil {
		t.Error("Review() with timeout should return error")
	}
	if !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Errorf("Review() error = %q, want context deadline exceeded", err.Error())
	}
}

func TestClient_Review_EmptyResponse(t *testing.T) {
	// Create mock server with empty choices
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mockResponse := openai.ChatCompletionResponse{
			ID:      "test-id",
			Object:  "chat.completion",
			Created: time.Now().Unix(),
			Model:   "anthropic/claude-3.5-sonnet",
			Choices: []openai.ChatCompletionChoice{},
			Usage: openai.Usage{
				PromptTokens:     100,
				CompletionTokens: 0,
				TotalTokens:      100,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(mockResponse)
	}))
	defer server.Close()

	logger := zaptest.NewLogger(t)
	cfg := &config.OpenRouterConfig{
		APIKey:  "test-key",
		BaseURL: server.URL,
		Timeout: 30,
	}

	openaiConfig := openai.DefaultConfig(cfg.APIKey)
	openaiConfig.BaseURL = cfg.BaseURL
	openaiClient := openai.NewClientWithConfig(openaiConfig)

	client := &Client{
		config: cfg,
		logger: logger,
		client: openaiClient,
	}

	ctx := context.Background()
	req := ReviewRequest{
		AgentName:    "Security",
		Model:        "anthropic/claude-3.5-sonnet",
		SystemPrompt: "You are a security reviewer",
		UserPrompt:   "Review this diff",
		MaxTokens:    2000,
		Temperature:  0.3,
	}

	_, err := client.Review(ctx, req)
	if err == nil {
		t.Error("Review() with empty choices should return error")
	}
	if !strings.Contains(err.Error(), "no response") {
		t.Errorf("Review() error = %q, want to contain 'no response'", err.Error())
	}
}

func TestClient_Review_TokenCounting(t *testing.T) {
	tests := []struct {
		name         string
		inputTokens  int
		outputTokens int
	}{
		{
			name:         "small response",
			inputTokens:  100,
			outputTokens: 50,
		},
		{
			name:         "large response",
			inputTokens:  2000,
			outputTokens: 1500,
		},
		{
			name:         "zero output",
			inputTokens:  100,
			outputTokens: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mockResponse := openai.ChatCompletionResponse{
					ID:      "test-id",
					Object:  "chat.completion",
					Created: time.Now().Unix(),
					Model:   "anthropic/claude-3.5-sonnet",
					Choices: []openai.ChatCompletionChoice{
						{
							Index: 0,
							Message: openai.ChatCompletionMessage{
								Role:    openai.ChatMessageRoleAssistant,
								Content: `{"status": "passed", "findings": [], "summary": "OK"}`,
							},
							FinishReason: openai.FinishReasonStop,
						},
					},
					Usage: openai.Usage{
						PromptTokens:     tt.inputTokens,
						CompletionTokens: tt.outputTokens,
						TotalTokens:      tt.inputTokens + tt.outputTokens,
					},
				}

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				json.NewEncoder(w).Encode(mockResponse)
			}))
			defer server.Close()

			logger := zaptest.NewLogger(t)
			cfg := &config.OpenRouterConfig{
				APIKey:  "test-key",
				BaseURL: server.URL,
				Timeout: 30,
			}

			openaiConfig := openai.DefaultConfig(cfg.APIKey)
			openaiConfig.BaseURL = cfg.BaseURL
			openaiClient := openai.NewClientWithConfig(openaiConfig)

			client := &Client{
				config: cfg,
				logger: logger,
				client: openaiClient,
			}

			ctx := context.Background()
			req := ReviewRequest{
				AgentName:    "Security",
				Model:        "anthropic/claude-3.5-sonnet",
				SystemPrompt: "You are a security reviewer",
				UserPrompt:   "Review this diff",
				MaxTokens:    2000,
				Temperature:  0.3,
			}

			resp, err := client.Review(ctx, req)
			if err != nil {
				t.Fatalf("Review() error = %v", err)
			}

			if resp.InputTokens != tt.inputTokens {
				t.Errorf("Review() InputTokens = %d, want %d", resp.InputTokens, tt.inputTokens)
			}
			if resp.OutputTokens != tt.outputTokens {
				t.Errorf("Review() OutputTokens = %d, want %d", resp.OutputTokens, tt.outputTokens)
			}
		})
	}
}

func TestReviewRequest_Validation(t *testing.T) {
	tests := []struct {
		name    string
		req     ReviewRequest
		wantLog string
	}{
		{
			name: "valid request",
			req: ReviewRequest{
				AgentName:    "Security",
				Model:        "anthropic/claude-3.5-sonnet",
				SystemPrompt: "System prompt",
				UserPrompt:   "User prompt",
				MaxTokens:    2000,
				Temperature:  0.3,
			},
			wantLog: "Security",
		},
		{
			name: "empty agent name",
			req: ReviewRequest{
				AgentName:    "",
				Model:        "anthropic/claude-3.5-sonnet",
				SystemPrompt: "System prompt",
				UserPrompt:   "User prompt",
				MaxTokens:    2000,
				Temperature:  0.3,
			},
			wantLog: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Verify request structure
			if tt.req.AgentName != tt.wantLog {
				if tt.wantLog != "" {
					t.Errorf("AgentName = %q, want %q", tt.req.AgentName, tt.wantLog)
				}
			}
			if tt.req.Model == "" {
				t.Error("Model should not be empty")
			}
			if tt.req.MaxTokens <= 0 {
				t.Error("MaxTokens should be positive")
			}
			if tt.req.Temperature < 0 || tt.req.Temperature > 1 {
				t.Error("Temperature should be between 0 and 1")
			}
		})
	}
}
