package activities

import (
	"encoding/json"
	"strings"
)

// StructuredReview represents the expected JSON structure from the LLM
type StructuredReview struct {
	Status   string    `json:"status"`   // "passed", "warning", "failed"
	Findings []Finding `json:"findings"` // Array of findings
	Summary  string    `json:"summary"`  // Overall assessment
}

// Finding represents a single review finding
type Finding struct {
	Severity     string `json:"severity"`               // "critical", "high", "medium", "low"
	Title        string `json:"title"`                   // Brief description
	Description  string `json:"description"`             // Detailed explanation
	File         string `json:"file,omitempty"`          // relative file path
	Line         int    `json:"line,omitempty"`           // best-effort line number
	SuggestedFix string `json:"suggested_fix,omitempty"` // review agent's proposed fix
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
			Findings: []Finding{
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
