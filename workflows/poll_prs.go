package workflows

import (
	"fmt"
	"time"

	enumspb "go.temporal.io/api/enums/v1"
	"github.com/rikdc/temporal-code-reviewer/activities"
	"github.com/rikdc/temporal-code-reviewer/types"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// PollPRsWorkflow is triggered by a Temporal Schedule. It lists open pull
// requests across all configured repos and starts a child PRReviewWorkflow for
// each PR that has not already been reviewed at its current HEAD SHA.
//
// Deduplication is handled at the GitHub layer: before starting a child
// workflow we check whether a PENDING review already exists for the current
// HEAD SHA. If one does, the PR is skipped. This approach survives worker
// restarts and allows re-review after a failed or incomplete earlier run.
// The child workflow policy is TERMINATE_IF_RUNNING so that a stale, stuck
// workflow for the same PR is replaced rather than blocking indefinitely.
func PollPRsWorkflow(ctx workflow.Context, input types.PollPRsInput) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("Poll PRs workflow started", "repos", input.Repos)

	allowlist := make(map[string]bool, len(input.AutoFixUsers))
	for _, u := range input.AutoFixUsers {
		allowlist[u] = true
	}

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 2,
		},
	}
	actCtx := workflow.WithActivityOptions(ctx, ao)

	for _, repo := range input.Repos {
		var prs []types.PRReviewInput
		if err := workflow.ExecuteActivity(actCtx, activities.ActivityListOpenPRs, repo).Get(ctx, &prs); err != nil {
			logger.Error("Failed to list PRs", "repo", repo, "error", err)
			continue
		}

		logger.Info("Listed open PRs", "repo", repo, "count", len(prs))

		for _, pr := range prs {
			pr.AutoFixEnabled = allowlist[pr.PRAuthor]

			// Check GitHub for an existing PENDING review at this SHA before
			// starting a new workflow. This is the deduplication gate: if a
			// review is already waiting for the author to submit, skip this PR.
			var alreadyReviewed bool
			if err := workflow.ExecuteActivity(actCtx, activities.ActivityHasPendingReview, pr).Get(ctx, &alreadyReviewed); err != nil {
				logger.Warn("Could not check for existing review, proceeding anyway",
					"pr_number", pr.PRNumber, "error", err)
			}
			if alreadyReviewed {
				logger.Info("Skipping PR — pending review already exists at this SHA",
					"pr_number", pr.PRNumber,
					"head_sha", pr.HeadSHA)
				continue
			}

			// Source prefix + short SHA makes the ID readable in the Temporal UI.
			shortSHA := pr.HeadSHA
			if len(shortSHA) > 8 {
				shortSHA = shortSHA[:8]
			}
			childID := fmt.Sprintf("pr-review/poller/%s/%s/%d/%s",
				pr.RepoOwner, pr.RepoName, pr.PRNumber, shortSHA)

			// TERMINATE_IF_RUNNING replaces any stale stuck workflow for the
			// same PR+SHA rather than blocking. Completed runs are not affected,
			// so re-reviews can start freely once the GitHub check above passes.
			cwo := workflow.ChildWorkflowOptions{
				WorkflowID:            childID,
				TaskQueue:             "pr-review-queue",
				ParentClosePolicy:     enumspb.PARENT_CLOSE_POLICY_ABANDON,
				WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_TERMINATE_IF_RUNNING,
			}

			f := workflow.ExecuteChildWorkflow(workflow.WithChildOptions(ctx, cwo), PRReviewWorkflow, pr)

			// Wait for the child to START (not complete). Without this the parent
			// can complete in the same workflow task before Temporal confirms the
			// child start, causing the child to be silently dropped.
			var we workflow.Execution
			if err := f.GetChildWorkflowExecution().Get(ctx, &we); err != nil {
				logger.Error("Failed to start PR review workflow",
					"workflow_id", childID,
					"pr_number", pr.PRNumber,
					"error", err)
				continue
			}

			logger.Info("Started PR review workflow",
				"workflow_id", childID,
				"run_id", we.RunID,
				"pr_number", pr.PRNumber,
				"pr_author", pr.PRAuthor,
				"auto_fix_enabled", pr.AutoFixEnabled)
		}
	}

	return nil
}
