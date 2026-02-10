package activities

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rikdc/temporal-code-reviewer/types"
	"go.uber.org/zap/zaptest"
)

// TestSecurityAgent_ParseResponse tests JSON parsing with valid and invalid inputs
func TestSecurityAgent_ParseResponse(t *testing.T) {
	tests := []struct {
		name           string
		content        string
		wantStatus     string
		wantFindings   int
		wantRawContent bool
		wantSummary    string
	}{
		{
			name: "valid JSON with passed status",
			content: `{
				"status": "passed",
				"findings": [],
				"summary": "No security issues detected"
			}`,
			wantStatus:     "passed",
			wantFindings:   0,
			wantRawContent: false,
			wantSummary:    "No security issues detected",
		},
		{
			name: "valid JSON with findings",
			content: `{
				"status": "failed",
				"findings": [
					{"severity": "critical", "title": "SQL Injection", "description": "Fix SQL query"},
					{"severity": "high", "title": "XSS Vulnerability", "description": "Sanitize input"}
				],
				"summary": "Found 2 critical issues"
			}`,
			wantStatus:     "failed",
			wantFindings:   2,
			wantRawContent: false,
			wantSummary:    "Found 2 critical issues",
		},
		{
			name:           "invalid JSON - plain text",
			content:        "This is not JSON but a plain text review",
			wantStatus:     "warning",
			wantFindings:   1,
			wantRawContent: true,
			wantSummary:    "Review completed but response format was not valid JSON",
		},
		{
			name:           "invalid JSON - malformed",
			content:        `{"status": "passed", "findings": [}`,
			wantStatus:     "warning",
			wantFindings:   1,
			wantRawContent: true,
		},
		{
			name:           "empty string",
			content:        "",
			wantStatus:     "warning",
			wantFindings:   1,
			wantRawContent: false, // Empty string is returned as rawContent but it's empty
		},
		{
			name: "valid JSON with warning status",
			content: `{
				"status": "warning",
				"findings": [
					{"severity": "medium", "title": "Minor Issue", "description": "Consider fixing"}
				],
				"summary": "Found 1 minor issue"
			}`,
			wantStatus:     "warning",
			wantFindings:   1,
			wantRawContent: false,
			wantSummary:    "Found 1 minor issue",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			review, rawContent := parseStructuredReview(tt.content, "Security")

			if review.Status != tt.wantStatus {
				t.Errorf("parseResponse() status = %q, want %q", review.Status, tt.wantStatus)
			}
			if len(review.Findings) != tt.wantFindings {
				t.Errorf("parseResponse() findings count = %d, want %d", len(review.Findings), tt.wantFindings)
			}
			if tt.wantSummary != "" && review.Summary != tt.wantSummary {
				t.Errorf("parseResponse() summary = %q, want %q", review.Summary, tt.wantSummary)
			}
			if tt.wantRawContent && rawContent == "" {
				t.Error("parseResponse() rawContent is empty, expected non-empty for invalid JSON")
			}
			if !tt.wantRawContent && rawContent != "" {
				t.Errorf("parseResponse() rawContent is non-empty (%q), expected empty for valid JSON", rawContent)
			}

			// Verify fallback structure for invalid JSON
			if tt.wantRawContent {
				if len(review.Findings) == 0 {
					t.Error("parseResponse() should have fallback finding for invalid JSON")
				}
				if review.Findings[0].Description != tt.content {
					t.Errorf("parseResponse() fallback description = %q, want %q", review.Findings[0].Description, tt.content)
				}
			}
		})
	}
}

// TestSecurityAgent_MapStatus tests status mapping
func TestSecurityAgent_MapStatus(t *testing.T) {
	agent := &SecurityAgent{}

	tests := []struct {
		input string
		want  string
	}{
		{"passed", types.StatusPassed},
		{"warning", types.StatusWarning},
		{"failed", types.StatusFailed},
		{"unknown", types.StatusWarning}, // Default to warning
		{"", types.StatusWarning},
		{"PASSED", types.StatusWarning}, // Case sensitive
		{"pass", types.StatusWarning},   // Exact match required
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := agent.mapStatus(tt.input)
			if got != tt.want {
				t.Errorf("mapStatus(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestSecurityAgent_SeverityEmoji tests severity emoji mapping
func TestSecurityAgent_SeverityEmoji(t *testing.T) {
	agent := &SecurityAgent{}

	tests := []struct {
		severity string
		want     string
	}{
		{"critical", "🚨"},
		{"high", "⚠️"},
		{"medium", "⚡"},
		{"low", "ℹ️"},
		{"unknown", "•"},
		{"", "•"},
		{"CRITICAL", "•"}, // Case sensitive
		{"info", "•"},     // Not a valid severity
	}

	for _, tt := range tests {
		t.Run(tt.severity, func(t *testing.T) {
			got := agent.severityEmoji(tt.severity)
			if got != tt.want {
				t.Errorf("severityEmoji(%q) = %q, want %q", tt.severity, got, tt.want)
			}
		})
	}
}

// TestSecurityAgent_FormatFindings tests findings formatting
func TestSecurityAgent_FormatFindings(t *testing.T) {
	logger := zaptest.NewLogger(t)
	agent := &SecurityAgent{Logger: logger}

	tests := []struct {
		name           string
		review         *StructuredReview
		rawContent     string
		wantContain    []string
		wantNotContain []string
		minCount       int
	}{
		{
			name: "with multiple findings",
			review: &StructuredReview{
				Status:  "failed",
				Summary: "Found critical issues",
				Findings: []Finding{
					{Severity: "critical", Title: "SQL Injection", Description: "Use prepared statements"},
					{Severity: "high", Title: "XSS Attack", Description: "Sanitize HTML output"},
				},
			},
			rawContent:  "",
			wantContain: []string{"Summary", "critical issues", "SQL Injection", "XSS Attack", "🚨", "⚠️"},
			minCount:    7, // Summary + blank + finding1 (title, desc, blank) + finding2 (title, desc, blank)
		},
		{
			name: "no findings - passed",
			review: &StructuredReview{
				Status:   "passed",
				Summary:  "All security checks passed",
				Findings: []Finding{},
			},
			rawContent:  "",
			wantContain: []string{"Summary", "All security checks passed", "No security issues found"},
			minCount:    3,
		},
		{
			name:        "fallback to raw content",
			review:      &StructuredReview{},
			rawContent:  "Plain text review: The code looks good",
			wantContain: []string{"not in expected JSON format", "Plain text review"},
			minCount:    3,
		},
		{
			name: "single medium severity finding",
			review: &StructuredReview{
				Status:  "warning",
				Summary: "Minor issue found",
				Findings: []Finding{
					{Severity: "medium", Title: "Hardcoded Secret", Description: "Move to environment variable"},
				},
			},
			rawContent:     "",
			wantContain:    []string{"Summary", "Minor issue", "medium", "Hardcoded Secret", "⚡"},
			wantNotContain: []string{"🚨", "⚠️"}, // Should not have critical/high emojis
			minCount:       4,
		},
		{
			name: "low severity finding",
			review: &StructuredReview{
				Status:  "warning",
				Summary: "Low priority issue",
				Findings: []Finding{
					{Severity: "low", Title: "Code Style", Description: "Consider refactoring"},
				},
			},
			rawContent:  "",
			wantContain: []string{"Low priority", "Code Style", "ℹ️"},
			minCount:    4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := agent.formatFindings(tt.review, tt.rawContent)

			if len(got) < tt.minCount {
				t.Errorf("formatFindings() length = %d, want at least %d", len(got), tt.minCount)
			}

			gotText := strings.Join(got, "\n")

			for _, want := range tt.wantContain {
				if !strings.Contains(gotText, want) {
					t.Errorf("formatFindings() should contain %q, got:\n%s", want, gotText)
				}
			}

			for _, notWant := range tt.wantNotContain {
				if strings.Contains(gotText, notWant) {
					t.Errorf("formatFindings() should NOT contain %q, got:\n%s", notWant, gotText)
				}
			}

			// Verify formatting structure
			if tt.rawContent == "" && len(tt.review.Findings) > 0 {
				// Should have emoji for each finding
				for _, f := range tt.review.Findings {
					emoji := agent.severityEmoji(f.Severity)
					if !strings.Contains(gotText, emoji) {
						t.Errorf("formatFindings() should contain emoji %q for severity %q", emoji, f.Severity)
					}
					if !strings.Contains(gotText, f.Title) {
						t.Errorf("formatFindings() should contain title %q", f.Title)
					}
					if !strings.Contains(gotText, f.Description) {
						t.Errorf("formatFindings() should contain description %q", f.Description)
					}
				}
			}
		})
	}
}

// TestSecurityAgent_FormatFindings_Edge tests edge cases
func TestSecurityAgent_FormatFindings_Edge(t *testing.T) {
	logger := zaptest.NewLogger(t)
	agent := &SecurityAgent{Logger: logger}

	t.Run("empty summary", func(t *testing.T) {
		review := &StructuredReview{
			Status:   "passed",
			Summary:  "",
			Findings: []Finding{},
		}
		got := agent.formatFindings(review, "")
		if len(got) == 0 {
			t.Error("formatFindings() should not be empty even with empty summary")
		}
	})

	t.Run("nil review with raw content", func(t *testing.T) {
		got := agent.formatFindings(&StructuredReview{}, "raw")
		if len(got) == 0 {
			t.Error("formatFindings() should not be empty with raw content")
		}
		gotText := strings.Join(got, "\n")
		if !strings.Contains(gotText, "raw") {
			t.Error("formatFindings() should contain raw content")
		}
	})

	t.Run("finding with empty fields", func(t *testing.T) {
		review := &StructuredReview{
			Status:  "warning",
			Summary: "Issues found",
			Findings: []Finding{
				{Severity: "", Title: "", Description: ""},
			},
		}
		got := agent.formatFindings(review, "")
		// Should still format without crashing
		if len(got) == 0 {
			t.Error("formatFindings() should handle empty finding fields")
		}
	})

	t.Run("very long description", func(t *testing.T) {
		longDesc := strings.Repeat("This is a very long description. ", 100)
		review := &StructuredReview{
			Status:  "failed",
			Summary: "Long finding",
			Findings: []Finding{
				{Severity: "high", Title: "Long Issue", Description: longDesc},
			},
		}
		got := agent.formatFindings(review, "")
		gotText := strings.Join(got, "\n")
		if !strings.Contains(gotText, longDesc) {
			t.Error("formatFindings() should handle long descriptions")
		}
	})
}

// TestStructuredReview_JSONMarshaling tests that the struct can be marshaled/unmarshaled
func TestStructuredReview_JSONMarshaling(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		wantErr bool
	}{
		{
			name:    "valid minimal JSON",
			json:    `{"status":"passed","findings":[],"summary":"OK"}`,
			wantErr: false,
		},
		{
			name: "valid with findings",
			json: `{
				"status": "failed",
				"findings": [
					{"severity": "high", "title": "Issue", "description": "Desc"}
				],
				"summary": "Failed"
			}`,
			wantErr: false,
		},
		{
			name:    "missing required field",
			json:    `{"status": "passed"}`,
			wantErr: false, // JSON unmarshaling doesn't fail, just uses zero values
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var review StructuredReview
			err := json.Unmarshal([]byte(tt.json), &review)
			if (err != nil) != tt.wantErr {
				t.Errorf("Unmarshal() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestFinding_Structure(t *testing.T) {
	// Test that Finding struct has expected fields
	finding := Finding{
		Severity:    "critical",
		Title:       "Test Issue",
		Description: "Test description",
	}

	if finding.Severity != "critical" {
		t.Errorf("Finding.Severity = %q, want 'critical'", finding.Severity)
	}
	if finding.Title != "Test Issue" {
		t.Errorf("Finding.Title = %q, want 'Test Issue'", finding.Title)
	}
	if finding.Description != "Test description" {
		t.Errorf("Finding.Description = %q, want 'Test description'", finding.Description)
	}
}
