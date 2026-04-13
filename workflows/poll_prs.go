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

			// Primary dedup gate: check the metrics DB. A review_run record means
			// we successfully posted a review for this exact commit, regardless
			// of whether the draft has since been submitted or deleted on GitHub.
			var reviewedInDB bool
			if err := workflow.ExecuteActivity(actCtx, activities.ActivityHasReviewedAtSHA, pr).Get(ctx, &reviewedInDB); err != nil {
				logger.Warn("Could not query metrics DB; falling through to GitHub check",
					"pr_number", pr.PRNumber, "error", err)
			}
			if reviewedInDB {
				logger.Info("Skipping PR — already reviewed at this SHA (metrics DB)",
					"pr_number", pr.PRNumber,
					"head_sha", pr.HeadSHA)
				continue
			}

			// Secondary gate: check GitHub for an in-flight PENDING review. This
			// catches reviews started by the webhook before the metrics DB has a
			// record (i.e. the workflow is still running and hasn't posted yet).
			var pendingOnGitHub bool
			if err := workflow.ExecuteActivity(actCtx, activities.ActivityHasPendingReview, pr).Get(ctx, &pendingOnGitHub); err != nil {
				logger.Warn("Could not check GitHub for pending review, proceeding anyway",
					"pr_number", pr.PRNumber, "error", err)
			}
			if pendingOnGitHub {
				logger.Info("Skipping PR — pending review already exists at this SHA (GitHub)",
					"pr_number", pr.PRNumber,
					"head_sha", pr.HeadSHA)
				continue
			}

			// Same ID format as the webhook handler so Temporal's reuse policy
			// deduplicates across both trigger sources.
			shortSHA := pr.HeadSHA
			if len(shortSHA) > 8 {
				shortSHA = shortSHA[:8]
			}
			childID := fmt.Sprintf("pr-review/%s/%s/%d/%s",
				pr.RepoOwner, pr.RepoName, pr.PRNumber, shortSHA)

			// ALLOW_DUPLICATE_FAILED_ONLY means: if a workflow for this PR+SHA
			// already completed successfully, do not start another one. Only
			// failed runs are retried. Since the SHA is in the workflow ID, a
			// new commit on the same PR gets a new ID and is always reviewed.
			// If a workflow is currently running (started by a previous poll
			// or the webhook) the start will error and we log and skip — the
			// HasPendingReview check above handles that case too.
			cwo := workflow.ChildWorkflowOptions{
				WorkflowID:            childID,
				TaskQueue:             "pr-review-queue",
				ParentClosePolicy:     enumspb.PARENT_CLOSE_POLICY_ABANDON,
				WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,
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
