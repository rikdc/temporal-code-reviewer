package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config represents the complete application configuration
type Config struct {
	OpenRouter   OpenRouterConfig `yaml:"openrouter"`
	Temporal     TemporalConfig   `yaml:"temporal"`
	Agents       AgentConfigs     `yaml:"agents"`
	Poller       PollerConfig     `yaml:"poller"`
	AutoFixUsers []string         `yaml:"auto_fix_users"` // GitHub logins that receive auto-fix PRs
}

// TemporalConfig holds Temporal server connection settings.
type TemporalConfig struct {
	// Namespace to use for all workflows. Defaults to "default" if empty.
	// For local dev, use a dedicated namespace (e.g. "code-reviewer") to reduce
	// clutter in the Temporal UI.
	Namespace string `yaml:"namespace"`
}

// PollerConfig holds configuration for the GitHub PR polling background process
type PollerConfig struct {
	Enabled  bool      `yaml:"enabled"`
	Interval int       `yaml:"interval_seconds"` // how often to poll, in seconds
	Repos    []string  `yaml:"repos"`            // list of "owner/repo" strings to watch
	Filters  PRFilters `yaml:"filters"`
}

// PRFilters controls which PRs the poller will submit for review.
type PRFilters struct {
	// MaxAgeDays skips PRs created more than this many days ago. 0 = no limit.
	MaxAgeDays int `yaml:"max_age_days"`
	// SkipDrafts skips PRs that are marked as drafts.
	SkipDrafts bool `yaml:"skip_drafts"`
	// SkipBots skips PRs authored by GitHub bot accounts (e.g. dependabot).
	SkipBots bool `yaml:"skip_bots"`
	// RequireReviewerLogins, when non-empty, only allows PRs where at least one
	// of the listed logins is a requested reviewer on the PR.
	RequireReviewerLogins []string `yaml:"require_reviewer_logins"`
}

// OpenRouterConfig holds OpenRouter API configuration
type OpenRouterConfig struct {
	APIKey  string `yaml:"api_key"`
	BaseURL string `yaml:"base_url"`
	Timeout int    `yaml:"timeout"`
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

// Load reads and parses the configuration file
// API key can be overridden via OPENROUTER_API_KEY environment variable
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	// Allow env var override (for local dev and security)
	if envKey := os.Getenv("OPENROUTER_API_KEY"); envKey != "" {
		cfg.OpenRouter.APIKey = envKey
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// Validate checks that the configuration is complete and valid
func (c *Config) Validate() error {
	if c.OpenRouter.APIKey == "" {
		return fmt.Errorf("openrouter.api_key required (set OPENROUTER_API_KEY env var)")
	}
	if c.OpenRouter.BaseURL == "" {
		return fmt.Errorf("openrouter.base_url required")
	}
	if c.OpenRouter.Timeout <= 0 {
		return fmt.Errorf("openrouter.timeout must be positive")
	}

	// Validate agent configurations
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

// agentsWithoutPromptFile lists agents that use a hardcoded system prompt rather than a file.
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
