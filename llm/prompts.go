package llm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// PromptSource is the interface used by review agents to load system prompts.
// The returned versionID identifies which A/B variant was selected; it is
// empty when falling back to the on-disk file.
type PromptSource interface {
	LoadForAgent(ctx context.Context, agentName, fallbackFile string) (content, versionID string, err error)
}

// PromptLoader loads prompt templates from the filesystem.
// It implements PromptSource as the baseline (no A/B testing).
type PromptLoader struct {
	basePath string
}

// NewPromptLoader creates a new PromptLoader with the specified base path
func NewPromptLoader(basePath string) *PromptLoader {
	return &PromptLoader{basePath: basePath}
}

// Load reads a prompt file and returns its content.
func (p *PromptLoader) Load(filename string) (string, error) {
	path := filepath.Join(p.basePath, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read prompt %s: %w", path, err)
	}
	return string(data), nil
}

// LoadForAgent implements PromptSource using on-disk files (no A/B variant).
func (p *PromptLoader) LoadForAgent(_ context.Context, _, fallbackFile string) (string, string, error) {
	content, err := p.Load(fallbackFile)
	return content, "", err
}
