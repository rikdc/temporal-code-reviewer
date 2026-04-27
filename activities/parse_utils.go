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
// value (object or array) by brace/bracket counting. This handles LLMs that
// append explanatory text after the JSON (e.g. "Human: ...") or that return a
// bare JSON array instead of a wrapper object.
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

	// Determine whether the outermost JSON value is an object or array by
	// finding which opening delimiter appears first in the content.
	objStart := strings.Index(content, "{")
	arrStart := strings.Index(content, "[")

	start := objStart
	var openCh, closeCh rune = '{', '}'
	if arrStart != -1 && (objStart == -1 || arrStart < objStart) {
		start = arrStart
		openCh, closeCh = '[', ']'
	}

	if start == -1 {
		return content
	}

	depth := 0
	inString := false
	escaped := false
	for i, ch := range content[start:] {
		switch {
		case escaped:
			escaped = false
		case ch == '\\' && inString:
			escaped = true
		case ch == '"':
			inString = !inString
		case !inString && ch == openCh:
			depth++
		case !inString && ch == closeCh:
			depth--
			if depth == 0 {
				return strings.TrimSpace(content[start : start+i+1])
			}
		}
	}

	return strings.TrimSpace(content[start:])
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
