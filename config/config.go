package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Config represents the complete application configuration
type Config struct {
	OpenRouter OpenRouterConfig `yaml:"openrouter"`
	Agents     AgentConfigs     `yaml:"agents"`
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
