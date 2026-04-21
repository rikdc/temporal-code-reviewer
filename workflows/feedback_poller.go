package workflows

import (
	"time"

	"github.com/rikdc/temporal-code-reviewer/activities"
	"github.com/rikdc/temporal-code-reviewer/types"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	feedbackPollInterval = 5 * time.Minute
	feedbackMaxPolls     = 2016 // ~7 days
)

// FeedbackPollerWorkflow polls a PR every 2 hours, recording deleted review
// comments as implicit false-positive feedback, until the PR is closed/merged
// or the safety limit is reached.
//
// It is started as a fire-and-forget child of PRReviewWorkflow with
// PARENT_CLOSE_POLICY_ABANDON so it outlives the parent.
func FeedbackPollerWorkflow(ctx workflow.Context, input types.FeedbackPollerInput) error {
	logger := workflow.GetLogger(ctx)
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 2,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	for i := 0; i < feedbackMaxPolls; i++ {
		var result types.FeedbackPollResult
		if err := workflow.ExecuteActivity(ctx, activities.ActivityCheckFeedback, input).Get(ctx, &result); err != nil {
			logger.Warn("Feedback poll activity failed", "error", err, "attempt", i+1)
		} else if result.PRClosed {
			logger.Info("PR is closed; stopping feedback poller",
				"pr_number", input.PRNumber, "polls", i+1)
			return nil
		}

		if i < feedbackMaxPolls-1 {
			workflow.Sleep(ctx, feedbackPollInterval)
		}
	}

	logger.Info("Feedback poller reached max polls; stopping",
		"pr_number", input.PRNumber, "max_polls", feedbackMaxPolls)
	return nil
}
