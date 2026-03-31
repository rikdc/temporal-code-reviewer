package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	tests := []struct {
		name        string
		yamlContent string
		envKey      string
		wantErr     bool
		errContains string
		validate    func(*testing.T, *Config)
	}{
		{
			name: "valid config with API key in YAML",
			yamlContent: `openrouter:
  api_key: "test-key-123"
  base_url: "https://openrouter.ai/api/v1"
  timeout: 30
agents:
  security:
    model: "anthropic/claude-3.5-sonnet"
    max_tokens: 2000
    temperature: 0.3
    prompt_file: "security.md"
  style:
    model: "anthropic/claude-3.5-haiku"
    max_tokens: 1500
    temperature: 0.5
    prompt_file: "style.md"
  logic:
    model: "anthropic/claude-3.5-sonnet"
    max_tokens: 2000
    temperature: 0.3
    prompt_file: "logic.md"
  documentation:
    model: "anthropic/claude-3.5-haiku"
    max_tokens: 1500
    temperature: 0.5
    prompt_file: "documentation.md"
  triage:
    model: "anthropic/claude-sonnet-4"
    max_tokens: 2000
    temperature: 0.2
    prompt_file: "triage.md"
  fix_generator:
    model: "anthropic/claude-haiku-4-5"
    max_tokens: 2000
    temperature: 0.1
    prompt_file: ""`,
			wantErr: false,
			validate: func(t *testing.T, cfg *Config) {
				if cfg.OpenRouter.APIKey != "test-key-123" {
					t.Errorf("APIKey = %q, want %q", cfg.OpenRouter.APIKey, "test-key-123")
				}
				if cfg.OpenRouter.BaseURL != "https://openrouter.ai/api/v1" {
					t.Errorf("BaseURL = %q, want %q", cfg.OpenRouter.BaseURL, "https://openrouter.ai/api/v1")
				}
				if cfg.OpenRouter.Timeout != 30 {
					t.Errorf("Timeout = %d, want 30", cfg.OpenRouter.Timeout)
				}
				if cfg.Agents.Security.Model != "anthropic/claude-3.5-sonnet" {
					t.Errorf("Security.Model = %q, want anthropic/claude-3.5-sonnet", cfg.Agents.Security.Model)
				}
			},
		},
		{
			name: "env var overrides YAML API key",
			yamlContent: `openrouter:
  api_key: "yaml-key"
  base_url: "https://openrouter.ai/api/v1"
  timeout: 30
agents:
  security:
    model: "anthropic/claude-3.5-sonnet"
    max_tokens: 2000
    temperature: 0.3
    prompt_file: "security.md"
  style:
    model: "anthropic/claude-3.5-haiku"
    max_tokens: 1500
    temperature: 0.5
    prompt_file: "style.md"
  logic:
    model: "anthropic/claude-3.5-sonnet"
    max_tokens: 2000
    temperature: 0.3
    prompt_file: "logic.md"
  documentation:
    model: "anthropic/claude-3.5-haiku"
    max_tokens: 1500
    temperature: 0.5
    prompt_file: "documentation.md"
  triage:
    model: "anthropic/claude-sonnet-4"
    max_tokens: 2000
    temperature: 0.2
    prompt_file: "triage.md"
  fix_generator:
    model: "anthropic/claude-haiku-4-5"
    max_tokens: 2000
    temperature: 0.1
    prompt_file: ""`,
			envKey:  "env-key-456",
			wantErr: false,
			validate: func(t *testing.T, cfg *Config) {
				if cfg.OpenRouter.APIKey != "env-key-456" {
					t.Errorf("APIKey = %q, want %q (env var should override)", cfg.OpenRouter.APIKey, "env-key-456")
				}
			},
		},
		{
			name: "missing API key",
			yamlContent: `openrouter:
  api_key: ""
  base_url: "https://openrouter.ai/api/v1"
  timeout: 30
agents:
  security:
    model: "anthropic/claude-3.5-sonnet"
    max_tokens: 2000
    temperature: 0.3
    prompt_file: "security.md"
  style:
    model: "anthropic/claude-3.5-haiku"
    max_tokens: 1500
    temperature: 0.5
    prompt_file: "style.md"
  logic:
    model: "anthropic/claude-3.5-sonnet"
    max_tokens: 2000
    temperature: 0.3
    prompt_file: "logic.md"
  documentation:
    model: "anthropic/claude-3.5-haiku"
    max_tokens: 1500
    temperature: 0.5
    prompt_file: "documentation.md"
  triage:
    model: "anthropic/claude-sonnet-4"
    max_tokens: 2000
    temperature: 0.2
    prompt_file: "triage.md"
  fix_generator:
    model: "anthropic/claude-haiku-4-5"
    max_tokens: 2000
    temperature: 0.1
    prompt_file: ""`,
			wantErr:     true,
			errContains: "api_key required",
		},
		{
			name: "missing base URL",
			yamlContent: `openrouter:
  api_key: "test-key"
  base_url: ""
  timeout: 30
agents:
  security:
    model: "anthropic/claude-3.5-sonnet"
    max_tokens: 2000
    temperature: 0.3
    prompt_file: "security.md"
  style:
    model: "anthropic/claude-3.5-haiku"
    max_tokens: 1500
    temperature: 0.5
    prompt_file: "style.md"
  logic:
    model: "anthropic/claude-3.5-sonnet"
    max_tokens: 2000
    temperature: 0.3
    prompt_file: "logic.md"
  documentation:
    model: "anthropic/claude-3.5-haiku"
    max_tokens: 1500
    temperature: 0.5
    prompt_file: "documentation.md"
  triage:
    model: "anthropic/claude-sonnet-4"
    max_tokens: 2000
    temperature: 0.2
    prompt_file: "triage.md"
  fix_generator:
    model: "anthropic/claude-haiku-4-5"
    max_tokens: 2000
    temperature: 0.1
    prompt_file: ""`,
			wantErr:     true,
			errContains: "base_url required",
		},
		{
			name: "invalid timeout",
			yamlContent: `openrouter:
  api_key: "test-key"
  base_url: "https://openrouter.ai/api/v1"
  timeout: 0
agents:
  security:
    model: "anthropic/claude-3.5-sonnet"
    max_tokens: 2000
    temperature: 0.3
    prompt_file: "security.md"
  style:
    model: "anthropic/claude-3.5-haiku"
    max_tokens: 1500
    temperature: 0.5
    prompt_file: "style.md"
  logic:
    model: "anthropic/claude-3.5-sonnet"
    max_tokens: 2000
    temperature: 0.3
    prompt_file: "logic.md"
  documentation:
    model: "anthropic/claude-3.5-haiku"
    max_tokens: 1500
    temperature: 0.5
    prompt_file: "documentation.md"
  triage:
    model: "anthropic/claude-sonnet-4"
    max_tokens: 2000
    temperature: 0.2
    prompt_file: "triage.md"
  fix_generator:
    model: "anthropic/claude-haiku-4-5"
    max_tokens: 2000
    temperature: 0.1
    prompt_file: ""`,
			wantErr:     true,
			errContains: "timeout must be positive",
		},
		{
			name: "missing agent model",
			yamlContent: `openrouter:
  api_key: "test-key"
  base_url: "https://openrouter.ai/api/v1"
  timeout: 30
agents:
  security:
    model: ""
    max_tokens: 2000
    temperature: 0.3
    prompt_file: "security.md"
  style:
    model: "anthropic/claude-3.5-haiku"
    max_tokens: 1500
    temperature: 0.5
    prompt_file: "style.md"
  logic:
    model: "anthropic/claude-3.5-sonnet"
    max_tokens: 2000
    temperature: 0.3
    prompt_file: "logic.md"
  documentation:
    model: "anthropic/claude-3.5-haiku"
    max_tokens: 1500
    temperature: 0.5
    prompt_file: "documentation.md"
  triage:
    model: "anthropic/claude-sonnet-4"
    max_tokens: 2000
    temperature: 0.2
    prompt_file: "triage.md"
  fix_generator:
    model: "anthropic/claude-haiku-4-5"
    max_tokens: 2000
    temperature: 0.1
    prompt_file: ""`,
			wantErr:     true,
			errContains: "security.model required",
		},
		{
			name: "invalid temperature",
			yamlContent: `openrouter:
  api_key: "test-key"
  base_url: "https://openrouter.ai/api/v1"
  timeout: 30
agents:
  security:
    model: "anthropic/claude-3.5-sonnet"
    max_tokens: 2000
    temperature: 1.5
    prompt_file: "security.md"
  style:
    model: "anthropic/claude-3.5-haiku"
    max_tokens: 1500
    temperature: 0.5
    prompt_file: "style.md"
  logic:
    model: "anthropic/claude-3.5-sonnet"
    max_tokens: 2000
    temperature: 0.3
    prompt_file: "logic.md"
  documentation:
    model: "anthropic/claude-3.5-haiku"
    max_tokens: 1500
    temperature: 0.5
    prompt_file: "documentation.md"
  triage:
    model: "anthropic/claude-sonnet-4"
    max_tokens: 2000
    temperature: 0.2
    prompt_file: "triage.md"
  fix_generator:
    model: "anthropic/claude-haiku-4-5"
    max_tokens: 2000
    temperature: 0.1
    prompt_file: ""`,
			wantErr:     true,
			errContains: "temperature must be between 0 and 1",
		},
		{
			name: "invalid max tokens",
			yamlContent: `openrouter:
  api_key: "test-key"
  base_url: "https://openrouter.ai/api/v1"
  timeout: 30
agents:
  security:
    model: "anthropic/claude-3.5-sonnet"
    max_tokens: -1
    temperature: 0.3
    prompt_file: "security.md"
  style:
    model: "anthropic/claude-3.5-haiku"
    max_tokens: 1500
    temperature: 0.5
    prompt_file: "style.md"
  logic:
    model: "anthropic/claude-3.5-sonnet"
    max_tokens: 2000
    temperature: 0.3
    prompt_file: "logic.md"
  documentation:
    model: "anthropic/claude-3.5-haiku"
    max_tokens: 1500
    temperature: 0.5
    prompt_file: "documentation.md"
  triage:
    model: "anthropic/claude-sonnet-4"
    max_tokens: 2000
    temperature: 0.2
    prompt_file: "triage.md"
  fix_generator:
    model: "anthropic/claude-haiku-4-5"
    max_tokens: 2000
    temperature: 0.1
    prompt_file: ""`,
			wantErr:     true,
			errContains: "max_tokens must be positive",
		},
		{
			name: "missing prompt file",
			yamlContent: `openrouter:
  api_key: "test-key"
  base_url: "https://openrouter.ai/api/v1"
  timeout: 30
agents:
  security:
    model: "anthropic/claude-3.5-sonnet"
    max_tokens: 2000
    temperature: 0.3
    prompt_file: ""
  style:
    model: "anthropic/claude-3.5-haiku"
    max_tokens: 1500
    temperature: 0.5
    prompt_file: "style.md"
  logic:
    model: "anthropic/claude-3.5-sonnet"
    max_tokens: 2000
    temperature: 0.3
    prompt_file: "logic.md"
  documentation:
    model: "anthropic/claude-3.5-haiku"
    max_tokens: 1500
    temperature: 0.5
    prompt_file: "documentation.md"
  triage:
    model: "anthropic/claude-sonnet-4"
    max_tokens: 2000
    temperature: 0.2
    prompt_file: "triage.md"
  fix_generator:
    model: "anthropic/claude-haiku-4-5"
    max_tokens: 2000
    temperature: 0.1
    prompt_file: ""`,
			wantErr:     true,
			errContains: "prompt_file required",
		},
		{
			name:        "invalid YAML",
			yamlContent: `invalid: yaml: content: [`,
			wantErr:     true,
			errContains: "parse config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp file with YAML content
			tmpDir := t.TempDir()
			configPath := filepath.Join(tmpDir, "config.yaml")
			if err := os.WriteFile(configPath, []byte(tt.yamlContent), 0644); err != nil {
				t.Fatalf("Failed to write temp config: %v", err)
			}

			// Set env var if specified
			if tt.envKey != "" {
				oldEnv := os.Getenv("OPENROUTER_API_KEY")
				if err := os.Setenv("OPENROUTER_API_KEY", tt.envKey); err != nil {
					t.Fatalf("Failed to set env var: %v", err)
				}
				defer func() {
					if err := os.Setenv("OPENROUTER_API_KEY", oldEnv); err != nil {
						t.Errorf("Failed to restore env var: %v", err)
					}
				}()
			} else {
				// Clear env var to avoid interference
				oldEnv := os.Getenv("OPENROUTER_API_KEY")
				if err := os.Unsetenv("OPENROUTER_API_KEY"); err != nil {
					t.Fatalf("Failed to unset env var: %v", err)
				}
				defer func() {
					if oldEnv != "" {
						if err := os.Setenv("OPENROUTER_API_KEY", oldEnv); err != nil {
							t.Errorf("Failed to restore env var: %v", err)
						}
					}
				}()
			}

			// Load config
			cfg, err := Load(configPath)

			// Check error expectation
			if tt.wantErr {
				if err == nil {
					t.Errorf("Load() error = nil, wantErr %v", tt.wantErr)
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("Load() error = %q, want to contain %q", err.Error(), tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("Load() unexpected error = %v", err)
				return
			}

			// Run validation if provided
			if tt.validate != nil {
				tt.validate(t, cfg)
			}
		})
	}
}

func TestLoadFileNotFound(t *testing.T) {
	_, err := Load("nonexistent.yaml")
	if err == nil {
		t.Error("Load() with nonexistent file should return error")
	}
	if !strings.Contains(err.Error(), "read config") {
		t.Errorf("Load() error = %q, want to contain 'read config'", err.Error())
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name        string
		config      Config
		wantErr     bool
		errContains string
	}{
		{
			name: "valid config",
			config: Config{
				OpenRouter: OpenRouterConfig{
					APIKey:  "test-key",
					BaseURL: "https://openrouter.ai/api/v1",
					Timeout: 30,
				},
				Agents: AgentConfigs{
					Security: AgentConfig{
						Model:       "anthropic/claude-3.5-sonnet",
						MaxTokens:   2000,
						Temperature: 0.3,
						PromptFile:  "prompts/security.md",
					},
					Style: AgentConfig{
						Model:       "anthropic/claude-3.5-haiku",
						MaxTokens:   1500,
						Temperature: 0.5,
						PromptFile:  "prompts/style.md",
					},
					Logic: AgentConfig{
						Model:       "anthropic/claude-3.5-sonnet",
						MaxTokens:   2000,
						Temperature: 0.3,
						PromptFile:  "prompts/logic.md",
					},
					Documentation: AgentConfig{
						Model:       "anthropic/claude-3.5-haiku",
						MaxTokens:   1500,
						Temperature: 0.5,
						PromptFile:  "prompts/documentation.md",
					},
					Triage: AgentConfig{
						Model:       "anthropic/claude-sonnet-4",
						MaxTokens:   2000,
						Temperature: 0.2,
						PromptFile:  "prompts/triage.md",
					},
					FixGenerator: AgentConfig{
						Model:       "anthropic/claude-haiku-4-5",
						MaxTokens:   2000,
						Temperature: 0.1,
						PromptFile:  "",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "missing API key",
			config: Config{
				OpenRouter: OpenRouterConfig{
					APIKey:  "",
					BaseURL: "https://openrouter.ai/api/v1",
					Timeout: 30,
				},
			},
			wantErr:     true,
			errContains: "api_key required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantErr {
				if err == nil {
					t.Errorf("Validate() error = nil, wantErr %v", tt.wantErr)
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("Validate() error = %q, want to contain %q", err.Error(), tt.errContains)
				}
			} else if err != nil {
				t.Errorf("Validate() unexpected error = %v", err)
			}
		})
	}
}

