package activities

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rikdc/temporal-code-reviewer/config"
	"github.com/rikdc/temporal-code-reviewer/events"
	"github.com/rikdc/temporal-code-reviewer/llm"
	"github.com/rikdc/temporal-code-reviewer/types"
	"go.temporal.io/sdk/activity"
	"go.uber.org/zap"
)

// TriageAgent classifies review findings as auto-fixable or human-required.
type TriageAgent struct {
	EventBus     events.Publisher
	Logger       *zap.Logger
	LLMClient    llm.Reviewer
	Config       *config.AgentConfig
	PromptLoader *llm.PromptLoader
}

// NewTriageAgent creates a new TriageAgent with the given dependencies.
func NewTriageAgent(eb events.Publisher, logger *zap.Logger, client llm.Reviewer, cfg *config.AgentConfig, pl *llm.PromptLoader) *TriageAgent {
	return &TriageAgent{
		EventBus:     eb,
		Logger:       logger,
		LLMClient:    client,
		Config:       cfg,
		PromptLoader: pl,
	}
}

// triageLLMResponse is the expected JSON structure from the triage LLM call.
type triageLLMResponse struct {
	Decisions []triageLLMDecision `json:"decisions"`
}

type triageLLMDecision struct {
	FindingTitle    string `json:"finding_title"`
	AutoFixable     bool   `json:"auto_fixable"`
	Reason          string `json:"reason"`
	FixInstructions string `json:"fix_instructions"`
}

// Execute classifies each finding as auto-fixable or human-required via an LLM call.
func (a *TriageAgent) Execute(ctx context.Context, input types.TriageInput) ([]types.TriageDecision, error) {
	info := activity.GetInfo(ctx)
	workflowID := info.WorkflowExecution.ID

	a.Logger.Info("Triage agent started",
		zap.String("workflow_id", workflowID),
		zap.Int("findings_count", len(input.Findings)))

	a.EventBus.Publish(types.WorkflowEvent{
		WorkflowID: workflowID,
		EventType:  types.EventAgentStarted,
		AgentName:  "Triage",
		Timestamp:  time.Now(),
	})

	// Progress: 25% - Load prompt
	activity.RecordHeartbeat(ctx, 25)
	a.publishProgress(workflowID, 25)

	systemPrompt, err := a.PromptLoader.Load("triage.md")
	if err != nil {
		return nil, a.handleError(workflowID, fmt.Errorf("load triage prompt: %w", err))
	}

	// Progress: 50% - Build user prompt and call LLM
	activity.RecordHeartbeat(ctx, 50)
	a.publishProgress(workflowID, 50)

	findingsJSON, err := json.Marshal(input.Findings)
	if err != nil {
		return nil, a.handleError(workflowID, fmt.Errorf("marshal findings: %w", err))
	}

	userPrompt := fmt.Sprintf(`Classify the following %d code review findings for PR #%d (%s/%s):

%s

Return a JSON object with a "decisions" array — one decision per finding.`,
		len(input.Findings),
		input.PRReviewInput.PRNumber,
		input.PRReviewInput.RepoOwner,
		input.PRReviewInput.RepoName,
		string(findingsJSON),
	)

	response, err := a.LLMClient.Review(ctx, llm.ReviewRequest{
		AgentName:    "Triage",
		Model:        a.Config.Model,
		SystemPrompt: systemPrompt,
		UserPrompt:   userPrompt,
		MaxTokens:    a.Config.MaxTokens,
		Temperature:  a.Config.Temperature,
	})
	if err != nil {
		return nil, a.handleError(workflowID, fmt.Errorf("llm triage: %w", err))
	}

	// Progress: 75% - Parse response
	activity.RecordHeartbeat(ctx, 75)
	a.publishProgress(workflowID, 75)

	cleaned := extractJSON(response.Content)
	var llmResp triageLLMResponse
	if err := json.Unmarshal([]byte(cleaned), &llmResp); err != nil {
		return nil, a.handleError(workflowID, fmt.Errorf("parse triage response: %w", err))
	}

	// Build a lookup from finding_title -> LLM decision
	decisionMap := make(map[string]triageLLMDecision, len(llmResp.Decisions))
	for _, d := range llmResp.Decisions {
		decisionMap[d.FindingTitle] = d
	}

	// Map LLM decisions back to findings; unmatched findings default to human-required
	decisions := make([]types.TriageDecision, 0, len(input.Findings))
	for _, finding := range input.Findings {
		d, ok := decisionMap[finding.Title]
		td := types.TriageDecision{
			Finding:         finding,
			AutoFixable:     d.AutoFixable,
			Reason:          d.Reason,
			FixInstructions: d.FixInstructions,
		}
		if !ok {
			td.AutoFixable = false
			td.Reason = "no matching triage decision from LLM — defaulting to human-required"
			td.FixInstructions = ""
		}
		decisions = append(decisions, td)
	}

	// Progress: 100% - Complete
	activity.RecordHeartbeat(ctx, 100)
	a.publishProgress(workflowID, 100)

	autoCount := countAutoFixable(decisions)

	result := &types.AgentResult{
		AgentName: "Triage",
		Status:    types.StatusCompleted,
		Findings:  formatTriageFindings(decisions, autoCount),
		Progress:  100,
		Timestamp: time.Now(),
	}

	a.EventBus.Publish(types.WorkflowEvent{
		WorkflowID: workflowID,
		EventType:  types.EventAgentCompleted,
		AgentName:  "Triage",
		Result:     result,
		Timestamp:  time.Now(),
	})

	a.Logger.Info("Triage agent completed",
		zap.String("workflow_id", workflowID),
		zap.Int("total", len(decisions)),
		zap.Int("auto_fixable", autoCount))

	return decisions, nil
}

func (a *TriageAgent) publishProgress(workflowID string, progress int) {
	a.EventBus.Publish(types.WorkflowEvent{
		WorkflowID: workflowID,
		EventType:  types.EventAgentProgress,
		AgentName:  "Triage",
		Progress:   progress,
		Timestamp:  time.Now(),
	})
}

func (a *TriageAgent) handleError(workflowID string, err error) error {
	a.Logger.Error("Triage agent failed",
		zap.String("workflow_id", workflowID),
		zap.Error(err))

	a.EventBus.Publish(types.WorkflowEvent{
		WorkflowID: workflowID,
		EventType:  types.EventAgentFailed,
		AgentName:  "Triage",
		Error:      err.Error(),
		Timestamp:  time.Now(),
	})

	return err
}

func formatTriageFindings(decisions []types.TriageDecision, autoCount int) []string {
	var findings []string
	humanCount := len(decisions) - autoCount

	findings = append(findings, fmt.Sprintf("**Triage Results:** %d auto-fixable, %d human-required", autoCount, humanCount))
	findings = append(findings, "")

	for _, d := range decisions {
		tag := "HUMAN"
		if d.AutoFixable {
			tag = "AUTO-FIX"
		}
		findings = append(findings, fmt.Sprintf("- [%s] **%s** — %s", tag, d.Finding.Title, d.Reason))
	}

	return findings
}

func countAutoFixable(decisions []types.TriageDecision) int {
	count := 0
	for _, d := range decisions {
		if d.AutoFixable {
			count++
		}
	}
	return count
}
