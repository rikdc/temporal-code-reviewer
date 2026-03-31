package activities

import (
	"testing"

	"github.com/rikdc/temporal-code-reviewer/types"
	"github.com/stretchr/testify/assert"
)

func TestBuildPRBody_DeferredItemsInCorrectSection(t *testing.T) {
	input := types.CreatePRInput{
		OriginalPRNum: 42,
		Changeset: types.CoalescedChangeset{
			Applied: []types.FixResult{
				{FindingID: "Fix Style Issue", FilesChanged: []string{"main.go"}},
			},
		},
		HumanRequired: []types.TriageDecision{
			{
				Finding: types.Finding{Title: "Auth Bypass", Severity: "critical"},
				Reason:  "critical security vulnerability",
			},
			{
				Finding: types.Finding{Title: "Race Condition", Severity: "high"},
				Reason:  "requires architectural analysis",
			},
		},
	}

	body := buildPRBody(input)

	// Deferred items should appear in the human-required section
	assert.Contains(t, body, "### Deferred — human review required (2)")
	assert.Contains(t, body, "**Auth Bypass** (critical)")
	assert.Contains(t, body, "**Race Condition** (high)")
	assert.Contains(t, body, "critical security vulnerability")

	// Applied items should appear in the applied section
	assert.Contains(t, body, "### Applied (1)")
	assert.Contains(t, body, "**Fix Style Issue**")
}

func TestBuildPRBody_ZeroCountSectionsOmitted(t *testing.T) {
	// Only applied, no human-required or conflicts
	input := types.CreatePRInput{
		OriginalPRNum: 10,
		Changeset: types.CoalescedChangeset{
			Applied: []types.FixResult{
				{FindingID: "Fix A", FilesChanged: []string{"a.go"}},
			},
		},
		HumanRequired: nil,
	}

	body := buildPRBody(input)

	assert.Contains(t, body, "### Applied (1)")
	assert.NotContains(t, body, "### Deferred")
	assert.NotContains(t, body, "### Conflicts")
}

func TestBuildPRBody_ConflictsSection(t *testing.T) {
	input := types.CreatePRInput{
		OriginalPRNum: 55,
		Changeset: types.CoalescedChangeset{
			Applied: []types.FixResult{
				{FindingID: "Good Fix", FilesChanged: []string{"ok.go"}},
			},
			Conflicts: []types.FixResult{
				{FindingID: "Conflicting Fix", FailureReason: "overlapping change to same file"},
			},
		},
	}

	body := buildPRBody(input)

	assert.Contains(t, body, "### Conflicts — skipped (1)")
	assert.Contains(t, body, "**Conflicting Fix** — overlapping change to same file")
}

func TestBuildPRBody_EmptyChangeset(t *testing.T) {
	input := types.CreatePRInput{
		OriginalPRNum: 1,
		Changeset:     types.CoalescedChangeset{},
		HumanRequired: []types.TriageDecision{
			{
				Finding: types.Finding{Title: "All Manual", Severity: "high"},
				Reason:  "complex refactor needed",
			},
		},
	}

	body := buildPRBody(input)

	assert.NotContains(t, body, "### Applied")
	assert.Contains(t, body, "### Deferred — human review required (1)")
	assert.Contains(t, body, "_Review each change before merging")
}

func TestBuildPRBody_PRNumberInBody(t *testing.T) {
	input := types.CreatePRInput{
		OriginalPRNum: 123,
		Changeset: types.CoalescedChangeset{
			Applied: []types.FixResult{
				{FindingID: "Fix", FilesChanged: []string{"x.go"}},
			},
		},
	}

	body := buildPRBody(input)
	assert.Contains(t, body, "PR #123")
}
