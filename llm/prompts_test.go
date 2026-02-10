package llm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewPromptLoader(t *testing.T) {
	loader := NewPromptLoader("/tmp/prompts")
	if loader == nil {
		t.Fatal("NewPromptLoader() returned nil")
	}
	if loader.basePath != "/tmp/prompts" {
		t.Errorf("NewPromptLoader() basePath = %q, want %q", loader.basePath, "/tmp/prompts")
	}
}

func TestPromptLoader_Load(t *testing.T) {
	// Create temp directory with test prompts
	tmpDir := t.TempDir()

	tests := []struct {
		name        string
		filename    string
		content     string
		wantErr     bool
		setupFile   bool
		errContains string
	}{
		{
			name:      "valid prompt file",
			filename:  "test.md",
			content:   "# Test Prompt\n\nThis is a test prompt.",
			setupFile: true,
			wantErr:   false,
		},
		{
			name:      "empty prompt file",
			filename:  "empty.md",
			content:   "",
			setupFile: true,
			wantErr:   false,
		},
		{
			name:      "large prompt file",
			filename:  "large.md",
			content:   string(make([]byte, 10000)),
			setupFile: true,
			wantErr:   false,
		},
		{
			name:        "missing file",
			filename:    "missing.md",
			setupFile:   false,
			wantErr:     true,
			errContains: "read prompt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup test file if needed
			if tt.setupFile {
				filePath := filepath.Join(tmpDir, tt.filename)
				if err := os.WriteFile(filePath, []byte(tt.content), 0644); err != nil {
					t.Fatalf("Failed to write test file: %v", err)
				}
			}

			// Load prompt
			loader := NewPromptLoader(tmpDir)
			got, err := loader.Load(tt.filename)

			// Check error expectation
			if tt.wantErr {
				if err == nil {
					t.Errorf("Load() error = nil, wantErr %v", tt.wantErr)
					return
				}
				if tt.errContains != "" && !contains(err.Error(), tt.errContains) {
					t.Errorf("Load() error = %q, want to contain %q", err.Error(), tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("Load() unexpected error = %v", err)
				return
			}

			if got != tt.content {
				t.Errorf("Load() = %q, want %q", got, tt.content)
			}
		})
	}
}

func TestPromptLoader_LoadFromSubdirectory(t *testing.T) {
	// Create temp directory with subdirectory
	tmpDir := t.TempDir()
	subDir := filepath.Join(tmpDir, "agents")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatalf("Failed to create subdirectory: %v", err)
	}

	// Create prompt file in subdirectory
	promptContent := "# Security Agent Prompt"
	promptPath := filepath.Join(subDir, "security.md")
	if err := os.WriteFile(promptPath, []byte(promptContent), 0644); err != nil {
		t.Fatalf("Failed to write prompt file: %v", err)
	}

	// Load with subdirectory
	loader := NewPromptLoader(tmpDir)
	got, err := loader.Load("agents/security.md")
	if err != nil {
		t.Errorf("Load() error = %v, want nil", err)
	}
	if got != promptContent {
		t.Errorf("Load() = %q, want %q", got, promptContent)
	}
}

func TestPromptLoader_LoadInvalidPath(t *testing.T) {
	loader := NewPromptLoader("/nonexistent/path")
	_, err := loader.Load("test.md")
	if err == nil {
		t.Error("Load() with invalid base path should return error")
	}
	if !contains(err.Error(), "read prompt") {
		t.Errorf("Load() error = %q, want to contain 'read prompt'", err.Error())
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && indexOf(s, substr) >= 0))
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
