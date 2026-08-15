package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config represents the complete application configuration
type Config struct {
	OpenRouter   OpenRouterConfig `yaml:"openrouter"`
	Temporal     TemporalConfig   `yaml:"temporal"`
	Agents       AgentConfigs     `yaml:"agents"`
	Poller       PollerConfig     `yaml:"poller"`
	AutoFixUsers []string         `yaml:"auto_fix_users"`
	Webhook      WebhookConfig    `yaml:"webhook"`
	Server       ServerConfig     `yaml:"server"`
	Admin        AdminConfig      `yaml:"admin"`
}

// ServerConfig holds HTTP server settings.
type ServerConfig struct {
	BindAddress      string `yaml:"bind_address"`
	DashboardAddress string `yaml:"dashboard_address"`
	ReadTimeout      int    `yaml:"read_timeout"`
	WriteTimeout     int    `yaml:"write_timeout"`
	IdleTimeout      int    `yaml:"idle_timeout"`
	MaxHeaderBytes   int    `yaml:"max_header_bytes"`
	MaxBodyBytes     int64  `yaml:"max_body_bytes"`
}

// WebhookConfig holds GitHub webhook settings.
type WebhookConfig struct {
	Enabled            bool     `yaml:"enabled"`
	Secret             string   `yaml:"secret"`
	SecretFile         string   `yaml:"secret_file"`
	AllowedRepos       []string `yaml:"allowed_repos"`
	MaxBodyBytes       int64    `yaml:"max_body_bytes"`
	AllowedActions     []string `yaml:"allowed_actions"`
	AllowLocalDiffTest bool     `yaml:"allow_local_diff_test"`
}

// AdminConfig holds administrative API settings.
type AdminConfig struct {
	Token     string `yaml:"token"`
	TokenFile string `yaml:"token_file"`
}

// TemporalConfig holds Temporal server connection settings.
type TemporalConfig struct {
	Namespace        string `yaml:"namespace"`
	DashboardBaseURL string `yaml:"dashboard_base_url"`
}

// PollerConfig holds configuration for the GitHub PR polling background process
type PollerConfig struct {
	Enabled  bool      `yaml:"enabled"`
	Interval int       `yaml:"interval_seconds"`
	Repos    []string  `yaml:"repos"`
	Filters  PRFilters `yaml:"filters"`
}

// PRFilters controls which PRs the poller will submit for review.
type PRFilters struct {
	MaxAgeDays           int      `yaml:"max_age_days"`
	SkipDrafts           bool     `yaml:"skip_drafts"`
	SkipBots             bool     `yaml:"skip_bots"`
	RequireReviewerLogins []string `yaml:"require_reviewer_logins"`
}

// OpenRouterConfig holds OpenRouter API configuration
type OpenRouterConfig struct {
	APIKey     string `yaml:"api_key"`
	APIKeyFile string `yaml:"api_key_file"`
	BaseURL    string `yaml:"base_url"`
	Timeout    int    `yaml:"timeout"`
}

// AgentConfigs holds configuration for all review agents
type AgentConfigs struct {
	Security      AgentConfig `yaml:"security"`
	Style         AgentConfig `yaml:"style"`
	Logic         AgentConfig `yaml:"logic"`
	Documentation AgentConfig `yaml:"documentation"`
	Triage        AgentConfig `yaml:"triage"`
	FixGenerator  AgentConfig `yaml:"fix_generator"`
}

// AgentConfig holds configuration for a single agent
type AgentConfig struct {
	Model       string  `yaml:"model"`
	MaxTokens   int     `yaml:"max_tokens"`
	Temperature float64 `yaml:"temperature"`
	PromptFile  string  `yaml:"prompt_file"`
}

// Load reads and parses the configuration file.
// Secrets can be provided via env vars or file-backed secrets.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Apply defaults
	applyDefaults(&cfg)

	// Resolve secrets from files
	if err := resolveSecrets(&cfg); err != nil {
		return nil, err
	}

	// Environment variable overrides (for development convenience)
	applyEnvOverrides(&cfg)

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func applyDefaults(cfg *Config) {
	if cfg.Server.BindAddress == "" {
		cfg.Server.BindAddress = "127.0.0.1:8082"
	}
	if cfg.Server.DashboardAddress == "" {
		cfg.Server.DashboardAddress = "127.0.0.1:8081"
	}
	if cfg.Server.ReadTimeout <= 0 {
		cfg.Server.ReadTimeout = 30
	}
	if cfg.Server.WriteTimeout <= 0 {
		cfg.Server.WriteTimeout = 60
	}
	if cfg.Server.IdleTimeout <= 0 {
		cfg.Server.IdleTimeout = 120
	}
	if cfg.Server.MaxHeaderBytes <= 0 {
		cfg.Server.MaxHeaderBytes = 1 << 20 // 1MB
	}
	if cfg.Server.MaxBodyBytes <= 0 {
		cfg.Server.MaxBodyBytes = 2 * 1024 * 1024 // 2MB
	}
	if cfg.Webhook.MaxBodyBytes <= 0 {
		cfg.Webhook.MaxBodyBytes = 2 * 1024 * 1024 // 2MB
	}
	if len(cfg.Webhook.AllowedActions) == 0 {
		cfg.Webhook.AllowedActions = []string{"opened", "synchronize", "reopened"}
	}
	if cfg.Poller.Interval <= 0 {
		cfg.Poller.Interval = 7200 // 2 hours
	}
}

func resolveSecrets(cfg *Config) error {
	// OpenRouter API key
	if cfg.OpenRouter.APIKeyFile != "" {
		key, err := readSecretFile(cfg.OpenRouter.APIKeyFile)
		if err != nil {
			return fmt.Errorf("read openrouter api_key_file: %w", err)
		}
		cfg.OpenRouter.APIKey = key
	}

	// Webhook secret
	if cfg.Webhook.SecretFile != "" {
		secret, err := readSecretFile(cfg.Webhook.SecretFile)
		if err != nil {
			return fmt.Errorf("read webhook secret_file: %w", err)
		}
		cfg.Webhook.Secret = secret
	}

	// Admin token
	if cfg.Admin.TokenFile != "" {
		token, err := readSecretFile(cfg.Admin.TokenFile)
		if err != nil {
			return fmt.Errorf("read admin token_file: %w", err)
		}
		cfg.Admin.Token = token
	}

	return nil
}

func readSecretFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(data), "\n\r"), nil
}

func applyEnvOverrides(cfg *Config) {
	if envKey := os.Getenv("OPENROUTER_API_KEY"); envKey != "" {
		cfg.OpenRouter.APIKey = envKey
	}
	if uiURL := os.Getenv("TEMPORAL_UI_URL"); uiURL != "" {
		cfg.Temporal.DashboardBaseURL = uiURL
	}
	if addr := os.Getenv("TEMPORAL_ADDRESS"); addr != "" {
		// Stored in main.go, but acknowledge here
	}
	if token := os.Getenv("ADMIN_API_TOKEN"); token != "" {
		cfg.Admin.Token = token
	}
	if secret := os.Getenv("WEBHOOK_SECRET"); secret != "" {
		cfg.Webhook.Secret = secret
	}
	if ghToken := os.Getenv("GITHUB_TOKEN"); ghToken != "" {
		// Used in main.go
	}
	if cfg.Temporal.DashboardBaseURL == "" {
		cfg.Temporal.DashboardBaseURL = "http://localhost:8081"
	}
}

// Validate checks that the configuration is complete and valid
func (c *Config) Validate() error {
	if c.OpenRouter.APIKey == "" {
		return fmt.Errorf("openrouter.api_key required (set OPENROUTER_API_KEY env var or openrouter.api_key_file)")
	}
	if c.OpenRouter.BaseURL == "" {
		return fmt.Errorf("openrouter.base_url required")
	}
	if c.OpenRouter.Timeout <= 0 {
		return fmt.Errorf("openrouter.timeout must be positive")
	}

	if c.Webhook.Enabled && c.Webhook.Secret == "" {
		return fmt.Errorf("webhook.secret required when webhook is enabled (set WEBHOOK_SECRET env var or webhook.secret_file)")
	}

	agents := map[string]AgentConfig{
		"security":      c.Agents.Security,
		"style":         c.Agents.Style,
		"logic":         c.Agents.Logic,
		"documentation": c.Agents.Documentation,
		"triage":        c.Agents.Triage,
		"fix_generator": c.Agents.FixGenerator,
	}

	for name, agent := range agents {
		if err := validateAgent(name, agent); err != nil {
			return err
		}
	}

	return nil
}

var agentsWithoutPromptFile = map[string]bool{
	"fix_generator": true,
}

func validateAgent(name string, agent AgentConfig) error {
	if agent.Model == "" {
		return fmt.Errorf("agents.%s.model required", name)
	}
	if agent.MaxTokens <= 0 {
		return fmt.Errorf("agents.%s.max_tokens must be positive", name)
	}
	if agent.Temperature < 0 || agent.Temperature > 1 {
		return fmt.Errorf("agents.%s.temperature must be between 0 and 1", name)
	}
	if agent.PromptFile == "" && !agentsWithoutPromptFile[name] {
		return fmt.Errorf("agents.%s.prompt_file required", name)
	}
	return nil
}
