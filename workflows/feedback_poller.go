package workflows

import (
	"time"

	"github.com/rikdc/temporal-code-reviewer/activities"
	"github.com/rikdc/temporal-code-reviewer/types"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

const (
	defaultFeedbackPollInterval = 2 * time.Hour
	feedbackMaxPolls            = 84 // 7 days at 2-hour intervals
)

// FeedbackPollerWorkflow polls a PR at a configurable interval, recording raw
// feedback observations from GitHub signals until the PR is closed/merged
// or the safety limit is reached.
//
// Feedback is stored as raw observations. The system does NOT interpret
// reactions, replies, or deleted comments as ground truth labels.
func FeedbackPollerWorkflow(ctx workflow.Context, input types.FeedbackPollerInput) error {
	logger := workflow.GetLogger(ctx)
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 2,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	pollInterval := defaultFeedbackPollInterval

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
			workflow.Sleep(ctx, pollInterval)
		}
	}

	logger.Info("Feedback poller reached max polls; stopping",
		"pr_number", input.PRNumber, "max_polls", feedbackMaxPolls)
	return nil
}
