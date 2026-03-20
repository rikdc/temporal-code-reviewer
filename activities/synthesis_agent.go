package activities

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rikdc/temporal-code-reviewer/events"
	"github.com/rikdc/temporal-code-reviewer/types"
	"go.temporal.io/sdk/activity"
	"go.uber.org/zap"
)

// SynthesisAgent aggregates results from all review agents
type SynthesisAgent struct {
	EventBus events.Publisher
	Logger   *zap.Logger
}

// Execute aggregates all agent results into a summary
func (a *SynthesisAgent) Execute(ctx context.Context, input types.SynthesisInput) (*types.ReviewSummary, error) {
	info := activity.GetInfo(ctx)
	workflowID := info.WorkflowExecution.ID

	// Publish start event
	a.EventBus.Publish(types.WorkflowEvent{
		WorkflowID: workflowID,
		EventType:  types.EventAgentStarted,
		AgentName:  "Synthesis",
		Timestamp:  time.Now(),
	})

	a.Logger.Info("Synthesis agent started",
		zap.String("workflow_id", workflowID),
		zap.Int("pr_number", input.PRReviewInput.PRNumber))

	// Simulate synthesis with progress updates (3-5 seconds)
	progressSteps := []int{0, 33, 66, 100}

	for _, progress := range progressSteps {
		activity.RecordHeartbeat(ctx, progress)

		a.EventBus.Publish(types.WorkflowEvent{
			WorkflowID: workflowID,
			EventType:  types.EventAgentProgress,
			AgentName:  "Synthesis",
			Progress:   progress,
			Timestamp:  time.Now(),
		})

		time.Sleep(1200 * time.Millisecond) // ~4 seconds total
	}

	// Analyze results
	overallStatus := "approved"
	hasFailures := false
	hasWarnings := false

	for _, result := range input.AgentResults {
		if result.Status == types.StatusFailed {
			hasFailures = true
		} else if result.Status == types.StatusWarning {
			hasWarnings = true
		}
	}

	if hasFailures {
		overallStatus = "blocked"
	} else if hasWarnings {
		overallStatus = "needs_changes"
	}

	// Generate summary
	summaryParts := []string{
		fmt.Sprintf("PR #%d: %s", input.PRReviewInput.PRNumber, input.PRReviewInput.Title),
		"",
		"Review Summary:",
	}

	for _, result := range input.AgentResults {
		summaryParts = append(summaryParts, fmt.Sprintf("• %s: %s", result.AgentName, result.Status))
	}

	summaryParts = append(summaryParts, "", fmt.Sprintf("Overall Status: %s", overallStatus))

	summary := &types.ReviewSummary{
		PRNumber:       input.PRReviewInput.PRNumber,
		OverallStatus:  overallStatus,
		Recommendation: strings.ToUpper(overallStatus),
		AgentResults:   input.AgentResults,
		Summary:        strings.Join(summaryParts, "\n"),
		Timestamp:      time.Now(),
	}

	// Create detailed findings for display in dashboard
	findings := []string{
		fmt.Sprintf("# PR Review Summary"),
		"",
		fmt.Sprintf("**PR #%d**: %s", input.PRReviewInput.PRNumber, input.PRReviewInput.Title),
		"",
		"## Agent Results",
		"",
	}

	// Add status for each agent
	for _, agentResult := range input.AgentResults {
		statusEmoji := getStatusEmoji(agentResult.Status)
		findings = append(findings, fmt.Sprintf("%s **%s**: %s", statusEmoji, agentResult.AgentName, agentResult.Status))
	}

	// Add overall recommendation
	findings = append(findings,
		"",
		"## Overall Recommendation",
		"",
		fmt.Sprintf("**Status**: %s", strings.ToUpper(overallStatus)),
	)

	result := &types.AgentResult{
		AgentName: "Synthesis",
		Status:    types.StatusCompleted,
		Findings:  findings,
		Progress:  100,
		Timestamp: time.Now(),
	}

	// Publish completion event
	a.EventBus.Publish(types.WorkflowEvent{
		WorkflowID: workflowID,
		EventType:  types.EventAgentCompleted,
		AgentName:  "Synthesis",
		Result:     result,
		Timestamp:  time.Now(),
	})

	a.Logger.Info("Synthesis agent completed",
		zap.String("workflow_id", workflowID),
		zap.String("overall_status", overallStatus))

	return summary, nil
}

// getStatusEmoji returns an emoji for the agent status
func getStatusEmoji(status string) string {
	switch status {
	case types.StatusPassed:
		return "✅"
	case types.StatusWarning:
		return "⚠️"
	case types.StatusFailed:
		return "❌"
	case types.StatusCompleted:
		return "✅"
	default:
		return "•"
	}
}
