package activities

import (
	"fmt"
	"testing"

	"github.com/rikdc/temporal-code-reviewer/types"
	"github.com/stretchr/testify/assert"
)

func TestApplyDiffBestEffort_NonOverlappingDiffs(t *testing.T) {
	original := `package main

import "fmt"

func main() {
	fmt.Println("hello")
	fmt.Println("world")
}
`
	// A simple diff adding a line
	diff := `--- a/main.go
+++ b/main.go
@@ -5,3 +5,4 @@
 func main() {
 	fmt.Println("hello")
+	fmt.Println("inserted")
 	fmt.Println("world")
`

	result := applyDiffBestEffort(original, diff)
	assert.Contains(t, result, "inserted")
	assert.Contains(t, result, "hello")
	assert.Contains(t, result, "world")
}

func TestApplyDiffBestEffort_EmptyDiff(t *testing.T) {
	original := "package main\n"
	result := applyDiffBestEffort(original, "")
	assert.Equal(t, original, result, "empty diff should return original")
}

func TestApplyDiffBestEffort_InvalidDiff(t *testing.T) {
	original := "package main\nfunc main() {}\n"
	result := applyDiffBestEffort(original, "not a diff at all")
	assert.Equal(t, original, result, "invalid diff should return original")
}

func TestBuildPRBody_AllSections(t *testing.T) {
	input := createTestPRInput(2, 1, 1)
	body := buildPRBody(input)

	assert.Contains(t, body, "## AI-implemented fixes")
	assert.Contains(t, body, "### Applied (2)")
	assert.Contains(t, body, "### Deferred — human review required (1)")
	assert.Contains(t, body, "### Conflicts — skipped (1)")
	assert.Contains(t, body, "PR #99")
}

func TestBuildPRBody_NoDeferred(t *testing.T) {
	input := createTestPRInput(1, 0, 0)
	body := buildPRBody(input)

	assert.Contains(t, body, "### Applied (1)")
	assert.NotContains(t, body, "### Deferred")
	assert.NotContains(t, body, "### Conflicts")
}

func TestBuildPRBody_NoApplied(t *testing.T) {
	input := createTestPRInput(0, 2, 0)
	body := buildPRBody(input)

	assert.NotContains(t, body, "### Applied")
	assert.Contains(t, body, "### Deferred — human review required (2)")
}

func TestBuildPRBody_OnlyConflicts(t *testing.T) {
	input := createTestPRInput(0, 0, 3)
	body := buildPRBody(input)

	assert.NotContains(t, body, "### Applied")
	assert.NotContains(t, body, "### Deferred")
	assert.Contains(t, body, "### Conflicts — skipped (3)")
}

func createTestPRInput(applied, humanRequired, conflicts int) types.CreatePRInput {
	input := types.CreatePRInput{
		OriginalPRNum: 99,
		RepoOwner:     "test",
		RepoName:      "repo",
	}

	for i := range applied {
		input.Changeset.Applied = append(input.Changeset.Applied, types.FixResult{
			FindingID:    fmt.Sprintf("Applied Fix %d", i+1),
			Success:      true,
			FilesChanged: []string{fmt.Sprintf("file%d.go", i+1)},
		})
	}

	for i := range humanRequired {
		input.HumanRequired = append(input.HumanRequired, types.TriageDecision{
			Finding: types.Finding{
				Title:    fmt.Sprintf("Human Finding %d", i+1),
				Severity: "high",
			},
			Reason: "requires human judgment",
		})
	}

	for i := range conflicts {
		input.Changeset.Conflicts = append(input.Changeset.Conflicts, types.FixResult{
			FindingID:     fmt.Sprintf("Conflict Fix %d", i+1),
			Success:       false,
			FailureReason: "overlapping change",
		})
	}

	return input
}
