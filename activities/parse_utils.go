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

// extractJSON strips markdown fences then extracts the first complete JSON
// object by brace counting. This handles LLMs that append explanatory text
// after the JSON (e.g. "Human: ...") which would otherwise break Unmarshal.
func extractJSON(content string) string {
	// Strip markdown code fences first.
	startIdx := strings.Index(content, "```json")
	offset := 7
	if startIdx == -1 {
		startIdx = strings.Index(content, "```")
		offset = 3
	}
	if startIdx != -1 {
		content = content[startIdx+offset:]
		if endIdx := strings.Index(content, "```"); endIdx != -1 {
			content = content[:endIdx]
		}
	}

	content = strings.TrimSpace(content)

	// Find the first '{' and walk to its matching '}', ignoring content in
	// strings. This discards any trailing text the LLM appended after the JSON.
	objStart := strings.Index(content, "{")
	if objStart == -1 {
		return content
	}

	depth := 0
	inString := false
	escaped := false
	for i, ch := range content[objStart:] {
		switch {
		case escaped:
			escaped = false
		case ch == '\\' && inString:
			escaped = true
		case ch == '"':
			inString = !inString
		case !inString && ch == '{':
			depth++
		case !inString && ch == '}':
			depth--
			if depth == 0 {
				return strings.TrimSpace(content[objStart : objStart+i+1])
			}
		}
	}

	return strings.TrimSpace(content[objStart:])
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
