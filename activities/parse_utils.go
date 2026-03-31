package activities

import (
	"encoding/json"
	"strings"

	"github.com/rikdc/temporal-code-reviewer/types"
)

// StructuredReview represents the expected JSON structure from the LLM
type StructuredReview struct {
	Status   string          `json:"status"`   // "passed", "warning", "failed"
	Findings []types.Finding `json:"findings"` // Array of findings
	Summary  string          `json:"summary"`  // Overall assessment
}

// extractJSON removes markdown code blocks and other common wrappers
func extractJSON(content string) string {
	// Remove markdown code blocks: ```json ... ``` or ``` ... ```
	if idx := strings.Index(content, "```json"); idx != -1 {
		content = content[idx+7:]
		if end := strings.Index(content, "```"); end != -1 {
			content = content[:end]
		}
	} else if idx := strings.Index(content, "```"); idx != -1 {
		content = content[idx+3:]
		if end := strings.Index(content, "```"); end != -1 {
			content = content[:end]
		}
	}

	// Trim whitespace
	return strings.TrimSpace(content)
}

// parseStructuredReview attempts to parse the LLM response as structured JSON
// Returns the parsed review and raw content as fallback
func parseStructuredReview(content string, agentName string) (*StructuredReview, string) {
	// Try to extract JSON from markdown code blocks if present
	cleaned := extractJSON(content)

	var review StructuredReview
	if err := json.Unmarshal([]byte(cleaned), &review); err != nil {
		// Return default structure with raw content
		return &StructuredReview{
			Status:  "warning",
			Summary: "Review completed but response format was not valid JSON",
			Findings: []types.Finding{
				{
					Severity:    "low",
					Title:       "Raw LLM Response",
					Description: content,
				},
			},
		}, content
	}
	return &review, ""
}
