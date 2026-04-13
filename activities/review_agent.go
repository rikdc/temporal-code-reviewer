package activities

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rikdc/temporal-code-reviewer/config"
	"github.com/rikdc/temporal-code-reviewer/events"
	"github.com/rikdc/temporal-code-reviewer/llm"
	"github.com/rikdc/temporal-code-reviewer/metrics"
	"github.com/rikdc/temporal-code-reviewer/types"
	"go.temporal.io/sdk/activity"
	"go.uber.org/zap"
)

// ReviewAgent is the shared base for all LLM-powered review agents.
// Specialised agents differ only by Name and ReviewFocus.
type ReviewAgent struct {
	Name         string
	ReviewFocus  string // e.g. "security vulnerabilities", "code style and quality"
	EventBus     events.Publisher
	Logger       *zap.Logger
	LLMClient    llm.Reviewer
	Config       *config.AgentConfig
	PromptSource llm.PromptSource
	MetricsRepo  metrics.Repository // may be nil
}

func NewSecurityAgent(eb events.Publisher, l *zap.Logger, c llm.Reviewer, cfg *config.AgentConfig, ps llm.PromptSource, mr metrics.Repository) *ReviewAgent {
	return &ReviewAgent{Name: "Security", ReviewFocus: "security vulnerabilities", EventBus: eb, Logger: l, LLMClient: c, Config: cfg, PromptSource: ps, MetricsRepo: mr}
}

func NewStyleAgent(eb events.Publisher, l *zap.Logger, c llm.Reviewer, cfg *config.AgentConfig, ps llm.PromptSource, mr metrics.Repository) *ReviewAgent {
	return &ReviewAgent{Name: "Style", ReviewFocus: "code style and quality", EventBus: eb, Logger: l, LLMClient: c, Config: cfg, PromptSource: ps, MetricsRepo: mr}
}

func NewLogicAgent(eb events.Publisher, l *zap.Logger, c llm.Reviewer, cfg *config.AgentConfig, ps llm.PromptSource, mr metrics.Repository) *ReviewAgent {
	return &ReviewAgent{Name: "Logic", ReviewFocus: "logic errors and correctness issues", EventBus: eb, Logger: l, LLMClient: c, Config: cfg, PromptSource: ps, MetricsRepo: mr}
}

func NewDocsAgent(eb events.Publisher, l *zap.Logger, c llm.Reviewer, cfg *config.AgentConfig, ps llm.PromptSource, mr metrics.Repository) *ReviewAgent {
	return &ReviewAgent{Name: "Documentation", ReviewFocus: "documentation quality", EventBus: eb, Logger: l, LLMClient: c, Config: cfg, PromptSource: ps, MetricsRepo: mr}
}

// Execute runs the LLM-powered review for this agent.
func (a *ReviewAgent) Execute(ctx context.Context, input types.AgentReviewInput) (*types.AgentResult, error) {
	info := activity.GetInfo(ctx)
	workflowID := info.WorkflowExecution.ID

	a.Logger.Info(a.Name+" agent started",
		zap.String("workflow_id", workflowID),
		zap.Int("pr_number", input.PRNumber),
		zap.String("model", a.Config.Model))

	a.EventBus.Publish(types.WorkflowEvent{
		WorkflowID: workflowID,
		EventType:  types.EventAgentStarted,
		AgentName:  a.Name,
		Timestamp:  time.Now(),
	})

	// Progress: 20% - Load prompt
	activity.RecordHeartbeat(ctx, 20)
	a.publishProgress(workflowID, 20)

	systemPrompt, promptVersionID, err := a.PromptSource.LoadForAgent(ctx, a.Name, a.Config.PromptFile)
	if err != nil {
		return a.handleError(workflowID, fmt.Errorf("load prompt: %w", err))
	}

	// Progress: 40% - Prepare user prompt
	activity.RecordHeartbeat(ctx, 40)
	a.publishProgress(workflowID, 40)

	userPrompt := fmt.Sprintf(`Review this Pull Request for %s:

**PR #%d: %s**
Repository: %s/%s

**Code Diff:**
%s

Analyze the code changes and return your findings in JSON format as specified in the system prompt.`,
		a.ReviewFocus,
		input.PRNumber,
		input.Title,
		input.RepoOwner,
		input.RepoName,
		input.DiffContent,
	)

	// Progress: 60% - Call LLM
	activity.RecordHeartbeat(ctx, 60)
	a.publishProgress(workflowID, 60)

	llmStart := time.Now()
	response, err := a.LLMClient.Review(ctx, llm.ReviewRequest{
		AgentName:    a.Name,
		Model:        a.Config.Model,
		SystemPrompt: systemPrompt,
		UserPrompt:   userPrompt,
		MaxTokens:    a.Config.MaxTokens,
		Temperature:  a.Config.Temperature,
	})
	if err != nil {
		return a.handleError(workflowID, fmt.Errorf("llm review: %w", err))
	}
	llmLatencyMS := time.Since(llmStart).Milliseconds()

	// Progress: 80% - Parse response
	activity.RecordHeartbeat(ctx, 80)
	a.publishProgress(workflowID, 80)

	review, rawContent := parseStructuredReview(response.Content, a.Name)
	if rawContent != "" {
		a.Logger.Warn("Failed to parse structured JSON response, using raw content",
			zap.String("workflow_id", workflowID))
	}

	result := &types.AgentResult{
		AgentName:          a.Name,
		Status:             mapStatus(review.Status),
		Findings:           formatFindings(a.Name, review, rawContent),
		StructuredFindings: review.Findings,
		Progress:           100,
		Timestamp:          time.Now(),
	}

	// Progress: 100% - Complete
	activity.RecordHeartbeat(ctx, 100)
	a.publishProgress(workflowID, 100)

	if a.MetricsRepo != nil {
		run := metrics.AgentRun{
			ID:              uuid.New().String(),
			ReviewRunID:     workflowID,
			AgentName:       a.Name,
			Status:          result.Status,
			Model:           a.Config.Model,
			InputTokens:     response.InputTokens,
			OutputTokens:    response.OutputTokens,
			LatencyMS:       llmLatencyMS,
			FindingsCount:   len(result.StructuredFindings),
			PromptVersionID: promptVersionID,
		}
		if err := a.MetricsRepo.SaveAgentRun(ctx, run); err != nil {
			a.Logger.Warn("Failed to save agent run metrics", zap.Error(err))
		}
	}

	a.EventBus.Publish(types.WorkflowEvent{
		WorkflowID: workflowID,
		EventType:  types.EventAgentCompleted,
		AgentName:  a.Name,
		Result:     result,
		Timestamp:  time.Now(),
	})

	a.Logger.Info(a.Name+" agent completed",
		zap.String("workflow_id", workflowID),
		zap.String("status", result.Status),
		zap.Int("findings_count", len(result.Findings)),
		zap.Int("input_tokens", response.InputTokens),
		zap.Int("output_tokens", response.OutputTokens))

	return result, nil
}

func (a *ReviewAgent) publishProgress(workflowID string, progress int) {
	a.EventBus.Publish(types.WorkflowEvent{
		WorkflowID: workflowID,
		EventType:  types.EventAgentProgress,
		AgentName:  a.Name,
		Progress:   progress,
		Timestamp:  time.Now(),
	})
}

func (a *ReviewAgent) handleError(workflowID string, err error) (*types.AgentResult, error) {
	a.Logger.Error(a.Name+" agent failed",
		zap.String("workflow_id", workflowID),
		zap.Error(err))

	a.EventBus.Publish(types.WorkflowEvent{
		WorkflowID: workflowID,
		EventType:  types.EventAgentFailed,
		AgentName:  a.Name,
		Error:      err.Error(),
		Timestamp:  time.Now(),
	})

	return nil, err
}

// mapStatus maps LLM status strings to types.Status constants.
func mapStatus(status string) string {
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

// severityEmoji returns an emoji for the severity level.
func severityEmoji(severity string) string {
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

// formatFindings converts structured findings to strings for AgentResult.
func formatFindings(agentName string, review *StructuredReview, rawContent string) []string {
	if rawContent != "" {
		return []string{
			"⚠️ Note: LLM response was not in expected JSON format",
			"",
			rawContent,
		}
	}

	findings := []string{fmt.Sprintf("**Summary:** %s", review.Summary), ""}

	if len(review.Findings) == 0 {
		findings = append(findings, fmt.Sprintf("✓ No %s issues found", lowercaseFirst(agentName)))
		return findings
	}

	for _, f := range review.Findings {
		emoji := severityEmoji(f.Severity)
		findings = append(findings, fmt.Sprintf("%s **[%s] %s**", emoji, f.Severity, f.Title))
		findings = append(findings, f.Description, "")
	}

	return findings
}

func lowercaseFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}
