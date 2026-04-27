package workflows

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"github.com/rikdc/temporal-code-reviewer/activities"
	"github.com/rikdc/temporal-code-reviewer/types"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// Search attribute keys used for filtering PR review workflows in the Temporal UI.
var (
	searchAttrRepository = temporal.NewSearchAttributeKeyString("Repository")
	searchAttrPRAuthor   = temporal.NewSearchAttributeKeyString("PRAuthor")
)

var (
	findingSeverityRe = regexp.MustCompile(`\*\*\[(\w+)\]\s+(.+?)\*\*`)
	nonAlphanumRe     = regexp.MustCompile(`[^a-zA-Z0-9-]`)
)

// PRReviewWorkflow orchestrates diff fetching, parallel agent execution, triage, and auto-fix.
func PRReviewWorkflow(ctx workflow.Context, input types.PRReviewInput) (*types.PRReviewResult, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("PR review workflow started", "pr_number", input.PRNumber)

	// Tag each execution with repo and author for easy filtering in the Temporal UI.
	// These custom attributes must be registered in the namespace before use:
	//   temporal operator search-attribute create --namespace code-reviewer \
	//     --name Repository --type Text
	//   temporal operator search-attribute create --namespace code-reviewer \
	//     --name PRAuthor --type Text
	_ = workflow.UpsertTypedSearchAttributes(ctx,
		searchAttrRepository.ValueSet(fmt.Sprintf("%s/%s", input.RepoOwner, input.RepoName)),
		searchAttrPRAuthor.ValueSet(input.PRAuthor),
	)

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

	// Resolve head SHA if the webhook didn't provide it.
	// The SHA is required for reading files at the correct commit during auto-fix.
	if input.HeadSHA == "" {
		shaOpts := workflow.ActivityOptions{
			StartToCloseTimeout: 15 * time.Second,
			RetryPolicy:         &temporal.RetryPolicy{MaximumAttempts: 2},
		}
		var sha string
		if err := workflow.ExecuteActivity(
			workflow.WithActivityOptions(ctx, shaOpts),
			activities.ActivityGetPRHeadSHA,
			input.RepoOwner, input.RepoName, input.PRNumber,
		).Get(ctx, &sha); err != nil {
			logger.Warn("Could not resolve PR head SHA; file reads will use branch name as fallback", "error", err)
		} else {
			input.HeadSHA = sha
			logger.Info("Resolved PR head SHA", "sha", sha)
		}
	}

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

	agentResults := []types.AgentResult{
		securityResult,
		styleResult,
		logicResult,
		docsResult,
	}

	// Phase 2: Synthesis agent aggregates results
	logger.Info("Starting synthesis agent")

	synthesisInput := types.SynthesisInput{
		PRReviewInput: input,
		AgentResults:  agentResults,
	}

	var summary types.ReviewSummary
	err = workflow.ExecuteActivity(ctx, activities.ActivitySynthesis, synthesisInput).Get(ctx, &summary)
	if err != nil {
		logger.Error("Synthesis agent failed", "error", err)
		return nil, err
	}

	logger.Info("Synthesis completed",
		"pr_number", input.PRNumber,
		"overall_status", summary.OverallStatus)

	// Phase 3: Post draft GitHub review — non-fatal, workflow continues on failure.
	// MaximumAttempts:1 prevents double-posting if the workflow is retried.
	postReviewOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 1,
		},
	}
	postReviewInput := types.PostReviewInput{
		PRReviewInput: input,
		AgentResults:  agentResults,
		Summary:       summary,
	}
	var postReviewOutput types.PostReviewOutput
	if err := workflow.ExecuteActivity(
		workflow.WithActivityOptions(ctx, postReviewOpts),
		activities.ActivityPostReview,
		postReviewInput,
	).Get(ctx, &postReviewOutput); err != nil {
		logger.Warn("Failed to post GitHub review — continuing", "error", err)
	}

	// Start the feedback poller immediately after posting the review so it
	// runs regardless of whether triage, auto-fix, or any later phase succeeds
	// or fails. The poller outlives the parent via PARENT_CLOSE_POLICY_ABANDON
	// and is idempotent — Temporal silently rejects a duplicate start for the
	// same workflow ID.
	workflow.ExecuteChildWorkflow(
		workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
			WorkflowID: fmt.Sprintf("feedback-poller-%s/%s#%d@%s",
				input.RepoOwner, input.RepoName, input.PRNumber, input.HeadSHA),
			ParentClosePolicy: enumspb.PARENT_CLOSE_POLICY_ABANDON,
		}),
		FeedbackPollerWorkflow,
		types.FeedbackPollerInput{
			WorkflowID:     workflow.GetInfo(ctx).WorkflowExecution.ID,
			RepoOwner:      input.RepoOwner,
			RepoName:       input.RepoName,
			PRNumber:       input.PRNumber,
			GitHubReviewID: postReviewOutput.GitHubReviewID,
			ReviewBody:     postReviewOutput.ReviewBody,
		},
	)

	// Phase 4: Triage — classify each finding as auto-fixable or human-required
	allFindings := flattenFindings(agentResults)
	logger.Info("Starting triage", "findings_count", len(allFindings))

	triageOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 120 * time.Second,
		HeartbeatTimeout:    30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 2,
			// Do not retry parse failures — they indicate a deterministic LLM
			// output problem (e.g. truncated JSON) that won't resolve on retry.
			NonRetryableErrorTypes: []string{"SyntaxError", "wrapError"},
		},
	}
	triageCtx := workflow.WithActivityOptions(ctx, triageOpts)

	var triageDecisions []types.TriageDecision
	triageInput := types.TriageInput{
		PRReviewInput: input,
		Findings:      allFindings,
	}
	if err := workflow.ExecuteActivity(triageCtx, activities.ActivityTriage, triageInput).Get(ctx, &triageDecisions); err != nil {
		return nil, fmt.Errorf("triage failed: %w", err)
	}

	// Split decisions
	var autoFixable, humanRequired []types.TriageDecision
	for _, d := range triageDecisions {
		if d.AutoFixable {
			autoFixable = append(autoFixable, d)
		} else {
			humanRequired = append(humanRequired, d)
		}
	}

	logger.Info("Triage completed",
		"auto_fixable", len(autoFixable),
		"human_required", len(humanRequired))

	// Phase 5: Fix fan-out — one child workflow per auto-fixable finding (only when caller opted in)
	if !input.AutoFixEnabled {
		logger.Info("Auto-fix disabled for this PR; skipping fix phases",
			"pr_author", input.PRAuthor)
		return &types.PRReviewResult{
			Summary: summary,
			Triage:  triageDecisions,
		}, nil
	}

	// (AutoFixEnabled == true from here down)
	var fixFutures []workflow.ChildWorkflowFuture
	for _, decision := range autoFixable {
		cwo := workflow.ChildWorkflowOptions{
			WorkflowID: fmt.Sprintf("%s-fix-%s", workflow.GetInfo(ctx).WorkflowExecution.ID, sanitise(decision.Finding.Title)),
			TaskQueue:  "pr-review-queue",
		}
		f := workflow.ExecuteChildWorkflow(
			workflow.WithChildOptions(ctx, cwo),
			FixFindingWorkflow,
			types.FixFindingInput{
				Decision:   decision,
				RepoOwner:  input.RepoOwner,
				RepoName:   input.RepoName,
				HeadBranch: input.HeadBranch,
				HeadSHA:    input.HeadSHA,
			},
		)
		fixFutures = append(fixFutures, f)
	}

	// Collect fix results
	var fixResults []types.FixResult
	for _, f := range fixFutures {
		var result types.FixResult
		if err := f.Get(ctx, &result); err != nil {
			fixResults = append(fixResults, types.FixResult{Success: false, FailureReason: err.Error()})
			continue
		}
		fixResults = append(fixResults, result)
	}

	// Phase 6: Coalesce
	coalesceOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 60 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 2,
		},
	}
	coalesceCtx := workflow.WithActivityOptions(ctx, coalesceOpts)

	var changeset types.CoalescedChangeset
	coalesceInput := types.CoalesceInput{
		FixResults: fixResults,
		RepoOwner:  input.RepoOwner,
		RepoName:   input.RepoName,
		HeadBranch: input.HeadBranch,
		HeadSHA:    input.HeadSHA,
		PRNumber:   input.PRNumber,
	}
	if err := workflow.ExecuteActivity(coalesceCtx, activities.ActivityCoalesce, coalesceInput).Get(ctx, &changeset); err != nil {
		return nil, fmt.Errorf("coalesce failed: %w", err)
	}

	result := &types.PRReviewResult{
		Summary:   summary,
		Triage:    triageDecisions,
		Changeset: changeset,
	}

	// Phase 7: PR creation — only if we have a branch to open against
	if changeset.BranchName != "" {
		createPROpts := workflow.ActivityOptions{
			StartToCloseTimeout: 30 * time.Second,
			RetryPolicy: &temporal.RetryPolicy{
				MaximumAttempts: 3,
			},
		}
		prCtx := workflow.WithActivityOptions(ctx, createPROpts)

		var prResult types.CreatePRResult
		createPRInput := types.CreatePRInput{
			Changeset:      changeset,
			OriginalPRNum:  input.PRNumber,
			OriginalBranch: input.HeadBranch,
			RepoOwner:      input.RepoOwner,
			RepoName:       input.RepoName,
			HumanRequired:  humanRequired,
		}
		if err := workflow.ExecuteActivity(prCtx, activities.ActivityCreatePR, createPRInput).Get(ctx, &prResult); err != nil {
			// Non-fatal: log and continue, workflow still returns triage results
			logger.Warn("PR creation failed", "error", err)
		} else {
			result.FixPRNumber = prResult.PRNumber
			result.FixPRURL = prResult.PRURL
		}
	}

	logger.Info("PR review workflow completed",
		"pr_number", input.PRNumber,
		"overall_status", summary.OverallStatus,
		"fix_pr", result.FixPRURL)

	return result, nil
}

// flattenFindings extracts typed findings from all agent results.
// Uses StructuredFindings (which include File, Line, SuggestedFix) when
// available, falling back to parsing formatted strings.
// Parse-failure placeholders (Title == "Raw LLM Response") are excluded so
// that downstream consumers (triage, metrics) never receive raw LLM output
// masquerading as a structured finding.
func flattenFindings(results []types.AgentResult) []types.Finding {
	var findings []types.Finding
	for _, r := range results {
		if len(r.StructuredFindings) > 0 {
			for _, f := range r.StructuredFindings {
				if f.Title != "Raw LLM Response" {
					findings = append(findings, f)
				}
			}
			continue
		}
		// Fallback: parse formatted strings for older results
		for _, f := range r.Findings {
			if f == "" || strings.HasPrefix(f, "**Summary:**") || strings.HasPrefix(f, "#") {
				continue
			}
			if finding, ok := parseFindingString(f); ok {
				findings = append(findings, finding)
			}
		}
	}
	return findings
}

// parseFindingString attempts to parse a formatted finding string back into a Finding struct.
func parseFindingString(s string) (types.Finding, bool) {
	// Match pattern: emoji **[severity] Title**
	matches := findingSeverityRe.FindStringSubmatch(s)
	if len(matches) >= 3 {
		return types.Finding{
			Severity:    matches[1],
			Title:       matches[2],
			Description: s,
		}, true
	}

	// Skip non-finding lines
	if strings.HasPrefix(s, "✓") || strings.HasPrefix(s, "⚠️ Note:") {
		return types.Finding{}, false
	}

	return types.Finding{}, false
}

// sanitise makes a string safe for use in a workflow ID.
func sanitise(s string) string {
	result := nonAlphanumRe.ReplaceAllString(s, "-")
	if len(result) > 50 {
		result = result[:50]
	}
	return strings.ToLower(result)
}
