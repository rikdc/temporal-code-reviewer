package activities

import (
	"context"

	"github.com/google/go-github/v68/github"
	"github.com/rikdc/temporal-code-reviewer/metrics"
	"github.com/rikdc/temporal-code-reviewer/types"
	"go.uber.org/zap"
)

// FeedbackPollerActivity checks which of our review comments still exist on
// GitHub and records deleted ones as implicit false-positive feedback.
type FeedbackPollerActivity struct {
	client      *github.Client
	metricsRepo metrics.Repository
	logger      *zap.Logger
}

// NewFeedbackPollerActivity creates a new FeedbackPollerActivity.
func NewFeedbackPollerActivity(client *github.Client, repo metrics.Repository, logger *zap.Logger) *FeedbackPollerActivity {
	return &FeedbackPollerActivity{client: client, metricsRepo: repo, logger: logger}
}

// CheckFeedback fetches the current state of the PR and its review comments,
// compares them against stored findings, and records any deleted comments as
// implicit false positives. Returns PRClosed=true when the PR is merged or
// closed, signalling the polling loop to stop.
func (a *FeedbackPollerActivity) CheckFeedback(ctx context.Context, input types.FeedbackPollerInput) (types.FeedbackPollResult, error) {
	// Check PR state.
	pr, _, err := a.client.PullRequests.Get(ctx, input.RepoOwner, input.RepoName, input.PRNumber)
	if err != nil {
		a.logger.Warn("Could not fetch PR state for feedback poll",
			zap.Int("pr_number", input.PRNumber), zap.Error(err))
		// Return without error — transient GitHub issues should not abort the poller.
		return types.FeedbackPollResult{}, nil
	}

	state := pr.GetState()
	if state == "closed" {
		return types.FeedbackPollResult{PRClosed: true}, nil
	}

	if input.GitHubReviewID == 0 {
		// Review ID not recorded yet (e.g., post failed). Nothing to check.
		return types.FeedbackPollResult{}, nil
	}

	// Fetch all comments still present on the review.
	liveComments, _, err := a.client.PullRequests.ListReviewComments(
		ctx, input.RepoOwner, input.RepoName, input.PRNumber, input.GitHubReviewID, nil,
	)
	if err != nil {
		a.logger.Warn("Could not list review comments for feedback",
			zap.Int64("review_id", input.GitHubReviewID), zap.Error(err))
		return types.FeedbackPollResult{}, nil
	}

	liveIDs := make(map[int64]bool, len(liveComments))
	for _, c := range liveComments {
		liveIDs[c.GetID()] = true
	}

	// Load all findings we recorded for this review.
	findings, err := a.metricsRepo.GetFindingsByReviewRun(ctx, input.WorkflowID)
	if err != nil {
		a.logger.Warn("Could not load findings for feedback poll", zap.Error(err))
		return types.FeedbackPollResult{}, nil
	}

	var deleted int
	for _, f := range findings {
		if f.GitHubCommentID == 0 {
			continue // body-only finding, no inline comment to track
		}
		if liveIDs[f.GitHubCommentID] {
			continue // still present
		}
		// Comment was deleted — record as implicit false positive.
		if err := a.metricsRepo.SaveFeedback(ctx, metrics.FeedbackEvent{
			FindingID: f.ID,
			Verdict:   "fp",
			Source:    "github_deleted",
		}); err != nil {
			a.logger.Warn("Failed to save implicit feedback", zap.String("finding_id", f.ID), zap.Error(err))
		} else {
			deleted++
		}
	}

	if deleted > 0 {
		a.logger.Info("Recorded implicit false positives from deleted comments",
			zap.Int("pr_number", input.PRNumber), zap.Int("count", deleted))
	}

	return types.FeedbackPollResult{DeletedComments: deleted}, nil
}
