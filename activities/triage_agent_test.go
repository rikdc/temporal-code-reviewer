package activities

import (
	"testing"

	"github.com/rikdc/temporal-code-reviewer/config"
	"github.com/rikdc/temporal-code-reviewer/llm"
	"github.com/rikdc/temporal-code-reviewer/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func TestTriageAgent_Execute_CriticalSecurityIsHumanRequired(t *testing.T) {
	pub := new(mockPublisher)
	pub.On("Publish", mock.AnythingOfType("types.WorkflowEvent")).Return()

	reviewer := new(mockReviewer)
	reviewer.On("Review", mock.Anything, mock.AnythingOfType("llm.ReviewRequest")).Return(&llm.ReviewResponse{
		Content: `{
			"decisions": [
				{
					"finding_title": "SQL Injection in user query",
					"auto_fixable": false,
					"reason": "Critical severity security vulnerability",
					"fix_instructions": ""
				}
			]
		}`,
		Model:        "test-model",
		InputTokens:  200,
		OutputTokens: 100,
	}, nil)

	promptDir := t.TempDir()
	require.NoError(t, writePromptFile(promptDir, "triage.md", "You are a triage agent."))

	agent := NewTriageAgent(pub, zap.NewNop(), reviewer, &config.AgentConfig{Model: "test-model", MaxTokens: 1000, Temperature: 0.2}, llm.NewPromptLoader(promptDir))

	env := newTestActivityEnv(t)
	env.RegisterActivity(agent.Execute)

	input := types.TriageInput{
		PRReviewInput: types.PRReviewInput{PRNumber: 42, RepoOwner: "rikdc", RepoName: "service"},
		Findings: []types.Finding{
			{
				Severity:    "critical",
				Title:       "SQL Injection in user query",
				Description: "User input directly in SQL",
				File:        "handlers/user.go",
				Line:        45,
			},
		},
	}

	val, err := env.ExecuteActivity(agent.Execute, input)
	require.NoError(t, err)

	var decisions []types.TriageDecision
	require.NoError(t, val.Get(&decisions))

	require.Len(t, decisions, 1)
	assert.False(t, decisions[0].AutoFixable, "critical security finding must not be auto-fixable")
	assert.Equal(t, "SQL Injection in user query", decisions[0].Finding.Title)
	assert.NotEmpty(t, decisions[0].Reason)
}

func TestTriageAgent_Execute_LowStyleIsAutoFixable(t *testing.T) {
	pub := new(mockPublisher)
	pub.On("Publish", mock.AnythingOfType("types.WorkflowEvent")).Return()

	reviewer := new(mockReviewer)
	reviewer.On("Review", mock.Anything, mock.AnythingOfType("llm.ReviewRequest")).Return(&llm.ReviewResponse{
		Content: `{
			"decisions": [
				{
					"finding_title": "Magic number should be constant",
					"auto_fixable": true,
					"reason": "Low severity, mechanical, single location",
					"fix_instructions": "Replace 86400 with a named constant"
				}
			]
		}`,
		Model:        "test-model",
		InputTokens:  150,
		OutputTokens: 80,
	}, nil)

	promptDir := t.TempDir()
	require.NoError(t, writePromptFile(promptDir, "triage.md", "You are a triage agent."))

	agent := NewTriageAgent(pub, zap.NewNop(), reviewer, &config.AgentConfig{Model: "test-model", MaxTokens: 1000, Temperature: 0.2}, llm.NewPromptLoader(promptDir))

	env := newTestActivityEnv(t)
	env.RegisterActivity(agent.Execute)

	input := types.TriageInput{
		PRReviewInput: types.PRReviewInput{PRNumber: 42, RepoOwner: "rikdc", RepoName: "service"},
		Findings: []types.Finding{
			{
				Severity:     "low",
				Title:        "Magic number should be constant",
				Description:  "Replace 86400 with constant",
				File:         "utils/time.go",
				Line:         89,
				SuggestedFix: "const SecondsPerDay = 86400",
			},
		},
	}

	val, err := env.ExecuteActivity(agent.Execute, input)
	require.NoError(t, err)

	var decisions []types.TriageDecision
	require.NoError(t, val.Get(&decisions))

	require.Len(t, decisions, 1)
	assert.True(t, decisions[0].AutoFixable, "low severity style finding should be auto-fixable")
	assert.NotEmpty(t, decisions[0].FixInstructions)
}

func TestTriageAgent_Execute_UnmatchedFindingDefaultsToHumanRequired(t *testing.T) {
	pub := new(mockPublisher)
	pub.On("Publish", mock.AnythingOfType("types.WorkflowEvent")).Return()

	// LLM returns empty decisions (partial output)
	reviewer := new(mockReviewer)
	reviewer.On("Review", mock.Anything, mock.AnythingOfType("llm.ReviewRequest")).Return(&llm.ReviewResponse{
		Content:      `{"decisions": []}`,
		Model:        "test-model",
		InputTokens:  100,
		OutputTokens: 20,
	}, nil)

	promptDir := t.TempDir()
	require.NoError(t, writePromptFile(promptDir, "triage.md", "You are a triage agent."))

	agent := NewTriageAgent(pub, zap.NewNop(), reviewer, &config.AgentConfig{Model: "test-model", MaxTokens: 1000, Temperature: 0.2}, llm.NewPromptLoader(promptDir))

	env := newTestActivityEnv(t)
	env.RegisterActivity(agent.Execute)

	input := types.TriageInput{
		PRReviewInput: types.PRReviewInput{PRNumber: 42, RepoOwner: "rikdc", RepoName: "service"},
		Findings: []types.Finding{
			{
				Severity:    "medium",
				Title:       "Some finding",
				Description: "Details",
			},
		},
	}

	val, err := env.ExecuteActivity(agent.Execute, input)
	require.NoError(t, err)

	var decisions []types.TriageDecision
	require.NoError(t, val.Get(&decisions))

	require.Len(t, decisions, 1)
	assert.False(t, decisions[0].AutoFixable, "unmatched finding should default to human-required")
	assert.Contains(t, decisions[0].Reason, "defaulting to human-required")
}

func TestTriageAgent_Execute_MultipleFindings(t *testing.T) {
	pub := new(mockPublisher)
	pub.On("Publish", mock.AnythingOfType("types.WorkflowEvent")).Return()

	reviewer := new(mockReviewer)
	reviewer.On("Review", mock.Anything, mock.AnythingOfType("llm.ReviewRequest")).Return(&llm.ReviewResponse{
		Content: `{
			"decisions": [
				{
					"finding_title": "Critical Auth Bypass",
					"auto_fixable": false,
					"reason": "Critical security issue",
					"fix_instructions": ""
				},
				{
					"finding_title": "Missing nil check",
					"auto_fixable": true,
					"reason": "Mechanical fix, single location",
					"fix_instructions": "Add nil check before access"
				},
				{
					"finding_title": "Missing godoc",
					"auto_fixable": true,
					"reason": "Mechanical documentation addition",
					"fix_instructions": "Add godoc comment"
				}
			]
		}`,
		Model:        "test-model",
		InputTokens:  300,
		OutputTokens: 150,
	}, nil)

	promptDir := t.TempDir()
	require.NoError(t, writePromptFile(promptDir, "triage.md", "You are a triage agent."))

	agent := NewTriageAgent(pub, zap.NewNop(), reviewer, &config.AgentConfig{Model: "test-model", MaxTokens: 1000, Temperature: 0.2}, llm.NewPromptLoader(promptDir))

	env := newTestActivityEnv(t)
	env.RegisterActivity(agent.Execute)

	input := types.TriageInput{
		PRReviewInput: types.PRReviewInput{PRNumber: 42, RepoOwner: "rikdc", RepoName: "service"},
		Findings: []types.Finding{
			{Severity: "critical", Title: "Critical Auth Bypass", Description: "Auth bypass found"},
			{Severity: "medium", Title: "Missing nil check", Description: "Nil deref possible", File: "handler.go", Line: 42},
			{Severity: "low", Title: "Missing godoc", Description: "Exported func undocumented", File: "api.go", Line: 10},
		},
	}

	val, err := env.ExecuteActivity(agent.Execute, input)
	require.NoError(t, err)

	var decisions []types.TriageDecision
	require.NoError(t, val.Get(&decisions))

	require.Len(t, decisions, 3)
	assert.False(t, decisions[0].AutoFixable)
	assert.True(t, decisions[1].AutoFixable)
	assert.True(t, decisions[2].AutoFixable)
}
