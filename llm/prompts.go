package llm

import (
	"fmt"
	"os"
	"path/filepath"
)

// PromptLoader loads prompt templates from the filesystem
type PromptLoader struct {
	basePath string
}

// NewPromptLoader creates a new PromptLoader with the specified base path
func NewPromptLoader(basePath string) *PromptLoader {
	return &PromptLoader{basePath: basePath}
}

// Load reads a prompt file and returns its content
func (p *PromptLoader) Load(filename string) (string, error) {
	path := filepath.Join(p.basePath, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read prompt %s: %w", path, err)
	}
	return string(data), nil
}
