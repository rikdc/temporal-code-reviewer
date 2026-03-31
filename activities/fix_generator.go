package activities

import (
	"context"
	"fmt"
	"strings"

	"github.com/rikdc/temporal-code-reviewer/config"
	"github.com/rikdc/temporal-code-reviewer/llm"
	"github.com/rikdc/temporal-code-reviewer/types"
	"go.uber.org/zap"
)

const maxFileContentBytes = 50 * 1024 // 50KB — matches the diff size cap elsewhere

// sensitiveFilePatterns lists path substrings that indicate files which must not
// be sent verbatim to an external LLM (credentials, keys, secrets, CI configs).
var sensitiveFilePatterns = []string{
	".env",
	"secret",
	"credential",
	"password",
	"private_key",
	".pem",
	".key",
	".p12",
	".pfx",
	".github/workflows",
}

// FixGeneratorActivity generates code fixes using an LLM.
type FixGeneratorActivity struct {
	LLMClient llm.Reviewer
	Config    *config.AgentConfig
	Logger    *zap.Logger
}

// NewFixGeneratorActivity creates a new FixGeneratorActivity.
func NewFixGeneratorActivity(client llm.Reviewer, cfg *config.AgentConfig, logger *zap.Logger) *FixGeneratorActivity {
	return &FixGeneratorActivity{
		LLMClient: client,
		Config:    cfg,
		Logger:    logger,
	}
}

// Execute generates a fix for a single finding by calling an LLM.
func (a *FixGeneratorActivity) Execute(ctx context.Context, input types.GenerateFixInput) (types.FixResult, error) {
	finding := input.Decision.Finding

	a.Logger.Info("Generating fix",
		zap.String("finding", finding.Title),
		zap.String("file", finding.File))

	// Refuse to send sensitive files to an external API.
	if isSensitivePath(finding.File) {
		a.Logger.Warn("Skipping fix generation: file matches sensitive path pattern",
			zap.String("file", finding.File))
		return types.FixResult{
			FindingID:     finding.Title,
			Success:       false,
			FailureReason: fmt.Sprintf("file %q matches a sensitive path pattern; skipped to prevent credential exposure", finding.File),
		}, nil
	}

	// Cap file content size before sending to the external API.
	fileContent := input.FileContent
	if len(fileContent) > maxFileContentBytes {
		a.Logger.Warn("File content truncated before LLM call",
			zap.String("file", finding.File),
			zap.Int("original_bytes", len(fileContent)),
			zap.Int("cap_bytes", maxFileContentBytes))
		fileContent = fileContent[:maxFileContentBytes]
	}

	systemPrompt := "You are a precise code editor. Apply exactly the fix described in fix_instructions. Do not change anything else. Return only a unified diff in standard format. No explanation, no markdown fences."

	userPrompt := fmt.Sprintf(`File: %s
Line: %d
Finding: %s
Description: %s
Fix Instructions: %s

Current file content:
%s`,
		finding.File,
		finding.Line,
		finding.Title,
		finding.Description,
		input.Decision.FixInstructions,
		fileContent,
	)

	response, err := a.LLMClient.Review(ctx, llm.ReviewRequest{
		AgentName:    "FixGenerator",
		Model:        a.Config.Model,
		SystemPrompt: systemPrompt,
		UserPrompt:   userPrompt,
		MaxTokens:    a.Config.MaxTokens,
		Temperature:  a.Config.Temperature,
	})
	if err != nil {
		return types.FixResult{
			FindingID:     finding.Title,
			Success:       false,
			FailureReason: fmt.Sprintf("LLM call failed: %v", err),
		}, nil
	}

	diff := strings.TrimSpace(response.Content)

	// Basic validation: a unified diff should contain at least --- and +++ lines
	if !isValidUnifiedDiff(diff) {
		return types.FixResult{
			FindingID:     finding.Title,
			Success:       false,
			FailureReason: "LLM response is not a valid unified diff",
		}, nil
	}

	commitMsg := fmt.Sprintf("fix: %s in %s", strings.ToLower(finding.Title), finding.File)

	return types.FixResult{
		FindingID:    finding.Title,
		Success:      true,
		Diff:         diff,
		FilesChanged: []string{finding.File},
		CommitMsg:    commitMsg,
	}, nil
}

// isSensitivePath reports whether filePath matches any known sensitive file pattern.
func isSensitivePath(filePath string) bool {
	lower := strings.ToLower(filePath)
	for _, pattern := range sensitiveFilePatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

// isValidUnifiedDiff performs basic validation that the string looks like a unified diff.
func isValidUnifiedDiff(diff string) bool {
	if diff == "" {
		return false
	}
	// A unified diff should have at least a --- or +++ line, or @@ hunk headers
	hasHeader := strings.Contains(diff, "---") || strings.Contains(diff, "+++")
	hasHunk := strings.Contains(diff, "@@")
	return hasHeader || hasHunk
}
