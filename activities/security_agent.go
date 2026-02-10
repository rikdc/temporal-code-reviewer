package activities

import (
	"context"
	"fmt"
	"time"

	"github.com/rikdc/temporal-code-reviewer/config"
	"github.com/rikdc/temporal-code-reviewer/events"
	"github.com/rikdc/temporal-code-reviewer/llm"
	"github.com/rikdc/temporal-code-reviewer/types"
	"go.temporal.io/sdk/activity"
	"go.uber.org/zap"
)

// SecurityAgent performs LLM-powered security analysis on PRs
type SecurityAgent struct {
	EventBus     *events.EventBus
	Logger       *zap.Logger
	LLMClient    *llm.Client
	Config       *config.AgentConfig
	PromptLoader *llm.PromptLoader
}

// Execute runs the LLM-powered security analysis
func (a *SecurityAgent) Execute(ctx context.Context, input types.AgentReviewInput) (*types.AgentResult, error) {
	info := activity.GetInfo(ctx)
	workflowID := info.WorkflowExecution.ID

	a.Logger.Info("Security agent started",
		zap.String("workflow_id", workflowID),
		zap.Int("pr_number", input.PRNumber),
		zap.String("model", a.Config.Model))

	// Publish start event
	a.EventBus.Publish(types.WorkflowEvent{
		WorkflowID: workflowID,
		EventType:  types.EventAgentStarted,
		AgentName:  "Security",
		Timestamp:  time.Now(),
	})

	// Progress: 20% - Load prompt
	activity.RecordHeartbeat(ctx, 20)
	a.publishProgress(workflowID, 20)

	systemPrompt, err := a.PromptLoader.Load(a.Config.PromptFile)
	if err != nil {
		return a.handleError(workflowID, fmt.Errorf("load prompt: %w", err))
	}

	// Progress: 40% - Prepare user prompt
	activity.RecordHeartbeat(ctx, 40)
	a.publishProgress(workflowID, 40)

	userPrompt := fmt.Sprintf(`Review this Pull Request for security vulnerabilities:

**PR #%d: %s**
Repository: %s/%s

**Code Diff:**
%s

Analyze the code changes and return your findings in JSON format as specified in the system prompt.`,
		input.PRNumber,
		input.Title,
		input.RepoOwner,
		input.RepoName,
		input.DiffContent,
	)

	// Progress: 60% - Call LLM
	activity.RecordHeartbeat(ctx, 60)
	a.publishProgress(workflowID, 60)

	response, err := a.LLMClient.Review(ctx, llm.ReviewRequest{
		AgentName:    "Security",
		Model:        a.Config.Model,
		SystemPrompt: systemPrompt,
		UserPrompt:   userPrompt,
		MaxTokens:    a.Config.MaxTokens,
		Temperature:  a.Config.Temperature,
	})
	if err != nil {
		return a.handleError(workflowID, fmt.Errorf("llm review: %w", err))
	}

	// Progress: 80% - Parse response
	activity.RecordHeartbeat(ctx, 80)
	a.publishProgress(workflowID, 80)

	review, rawContent := parseStructuredReview(response.Content, "Security")
	if rawContent != "" {
		a.Logger.Warn("Failed to parse structured JSON response, using raw content",
			zap.String("workflow_id", workflowID))
	}

	// Convert to AgentResult
	result := &types.AgentResult{
		AgentName: "Security",
		Status:    a.mapStatus(review.Status),
		Findings:  a.formatFindings(review, rawContent),
		Progress:  100,
		Timestamp: time.Now(),
	}

	// Progress: 100% - Complete
	activity.RecordHeartbeat(ctx, 100)
	a.publishProgress(workflowID, 100)

	// Publish completion event
	a.EventBus.Publish(types.WorkflowEvent{
		WorkflowID: workflowID,
		EventType:  types.EventAgentCompleted,
		AgentName:  "Security",
		Result:     result,
		Timestamp:  time.Now(),
	})

	a.Logger.Info("Security agent completed",
		zap.String("workflow_id", workflowID),
		zap.String("status", result.Status),
		zap.Int("findings_count", len(result.Findings)),
		zap.Int("input_tokens", response.InputTokens),
		zap.Int("output_tokens", response.OutputTokens))

	return result, nil
}

// formatFindings converts structured findings to strings for AgentResult
func (a *SecurityAgent) formatFindings(review *StructuredReview, rawContent string) []string {
	if rawContent != "" {
		// Fallback to raw content
		return []string{
			"⚠️ Note: LLM response was not in expected JSON format",
			"",
			rawContent,
		}
	}

	findings := []string{fmt.Sprintf("**Summary:** %s", review.Summary), ""}

	if len(review.Findings) == 0 {
		findings = append(findings, "✓ No security issues found")
		return findings
	}

	for _, f := range review.Findings {
		emoji := a.severityEmoji(f.Severity)
		findings = append(findings, fmt.Sprintf("%s **[%s] %s**", emoji, f.Severity, f.Title))
		findings = append(findings, f.Description, "")
	}

	return findings
}

// mapStatus maps LLM status to types.Status constants
func (a *SecurityAgent) mapStatus(status string) string {
	switch status {
	case "passed":
		return types.StatusPassed
	case "warning":
		return types.StatusWarning
	case "failed":
		return types.StatusFailed
	default:
		return types.StatusWarning
	}
}

// severityEmoji returns an emoji for the severity level
func (a *SecurityAgent) severityEmoji(severity string) string {
	switch severity {
	case "critical":
		return "🚨"
	case "high":
		return "⚠️"
	case "medium":
		return "⚡"
	case "low":
		return "ℹ️"
	default:
		return "•"
	}
}

// publishProgress publishes a progress event
func (a *SecurityAgent) publishProgress(workflowID string, progress int) {
	a.EventBus.Publish(types.WorkflowEvent{
		WorkflowID: workflowID,
		EventType:  types.EventAgentProgress,
		AgentName:  "Security",
		Progress:   progress,
		Timestamp:  time.Now(),
	})
}

// handleError publishes an error event and returns an error result
func (a *SecurityAgent) handleError(workflowID string, err error) (*types.AgentResult, error) {
	a.Logger.Error("Security agent failed",
		zap.String("workflow_id", workflowID),
		zap.Error(err))

	a.EventBus.Publish(types.WorkflowEvent{
		WorkflowID: workflowID,
		EventType:  types.EventAgentFailed,
		AgentName:  "Security",
		Error:      err.Error(),
		Timestamp:  time.Now(),
	})

	return nil, err
}
