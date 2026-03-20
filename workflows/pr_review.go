package workflows

import (
	"time"

	"github.com/rikdc/temporal-code-reviewer/activities"
	"github.com/rikdc/temporal-code-reviewer/types"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// PRReviewWorkflow orchestrates diff fetching and parallel agent execution
func PRReviewWorkflow(ctx workflow.Context, input types.PRReviewInput) (*types.ReviewSummary, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("PR review workflow started", "pr_number", input.PRNumber)

	// Configure activity options with 90s timeout for LLM calls
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 90 * time.Second,
		HeartbeatTimeout:    30 * time.Second, // LLM calls can take 10-20s
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// Phase 0: Fetch diff content (cached)
	logger.Info("Fetching PR diff", "diff_url", input.DiffURL)

	var diffContent string
	err := workflow.ExecuteActivity(ctx, activities.ActivityDiffFetcher, input.DiffURL).Get(ctx, &diffContent)
	if err != nil {
		logger.Error("Diff fetch failed", "error", err)
		return nil, err
	}

	logger.Info("Diff fetched successfully", "size", len(diffContent))

	// Create AgentReviewInput with diff content
	agentInput := types.AgentReviewInput{
		PRReviewInput: input,
		DiffContent:   diffContent,
	}

	// Phase 1: Launch 4 agents in parallel
	logger.Info("Launching parallel review agents")

	// Call activities by their full method names
	securityFuture := workflow.ExecuteActivity(ctx, activities.ActivitySecurity, agentInput)
	styleFuture := workflow.ExecuteActivity(ctx, activities.ActivityStyle, agentInput)
	logicFuture := workflow.ExecuteActivity(ctx, activities.ActivityLogic, agentInput)
	docsFuture := workflow.ExecuteActivity(ctx, activities.ActivityDocs, agentInput)

	// Wait for all agents to complete
	var securityResult, styleResult, logicResult, docsResult types.AgentResult

	if err := securityFuture.Get(ctx, &securityResult); err != nil {
		logger.Error("Security agent failed", "error", err)
		return nil, err
	}

	if err := styleFuture.Get(ctx, &styleResult); err != nil {
		logger.Error("Style agent failed", "error", err)
		return nil, err
	}

	if err := logicFuture.Get(ctx, &logicResult); err != nil {
		logger.Error("Logic agent failed", "error", err)
		return nil, err
	}

	if err := docsFuture.Get(ctx, &docsResult); err != nil {
		logger.Error("Docs agent failed", "error", err)
		return nil, err
	}

	logger.Info("All review agents completed",
		"security_status", securityResult.Status,
		"style_status", styleResult.Status,
		"logic_status", logicResult.Status,
		"docs_status", docsResult.Status)

	// Phase 2: Synthesis agent aggregates results
	logger.Info("Starting synthesis agent")

	synthesisInput := types.SynthesisInput{
		PRReviewInput: input,
		AgentResults: []types.AgentResult{
			securityResult,
			styleResult,
			logicResult,
			docsResult,
		},
	}

	var summary types.ReviewSummary
	err = workflow.ExecuteActivity(ctx, activities.ActivitySynthesis, synthesisInput).Get(ctx, &summary)
	if err != nil {
		logger.Error("Synthesis agent failed", "error", err)
		return nil, err
	}

	logger.Info("PR review workflow completed",
		"pr_number", input.PRNumber,
		"overall_status", summary.OverallStatus)

	return &summary, nil
}
