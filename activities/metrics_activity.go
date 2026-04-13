package activities

import (
	"context"

	"github.com/rikdc/temporal-code-reviewer/metrics"
	"github.com/rikdc/temporal-code-reviewer/types"
	"go.uber.org/zap"
)

// MetricsActivity exposes metrics store queries as Temporal activities.
type MetricsActivity struct {
	repo   metrics.Repository
	logger *zap.Logger
}

// NewMetricsActivity creates a new MetricsActivity.
func NewMetricsActivity(repo metrics.Repository, logger *zap.Logger) *MetricsActivity {
	return &MetricsActivity{repo: repo, logger: logger}
}

// HasReviewedAtSHA returns true if the metrics store contains a review_run
// or pr_skips record for the given PR at the given HEAD SHA. The poller uses
// this as its primary dedup gate, independent of GitHub's pending-review state.
func (a *MetricsActivity) HasReviewedAtSHA(ctx context.Context, pr types.PRReviewInput) (bool, error) {
	reviewed, err := a.repo.HasReviewedAtSHA(ctx, pr.RepoOwner, pr.RepoName, pr.PRNumber, pr.HeadSHA)
	if err != nil {
		a.logger.Warn("Failed to query metrics for HasReviewedAtSHA; proceeding without DB check",
			zap.String("repo", pr.RepoOwner+"/"+pr.RepoName),
			zap.Int("pr_number", pr.PRNumber),
			zap.Error(err))
		return false, nil // non-fatal: fall through to HasPendingReview
	}
	return reviewed, nil
}

// RecordSkip marks a PR+SHA as explicitly skipped so the poller will not
// trigger a re-review. Call this when discarding a review you do not want
// re-generated for the same commit.
func (a *MetricsActivity) RecordSkip(ctx context.Context, pr types.PRReviewInput) error {
	if err := a.repo.RecordSkip(ctx, pr.RepoOwner, pr.RepoName, pr.PRNumber, pr.HeadSHA); err != nil {
		a.logger.Error("Failed to record PR skip",
			zap.String("repo", pr.RepoOwner+"/"+pr.RepoName),
			zap.Int("pr_number", pr.PRNumber),
			zap.String("head_sha", pr.HeadSHA),
			zap.Error(err))
		return err
	}
	a.logger.Info("Recorded PR skip",
		zap.String("repo", pr.RepoOwner+"/"+pr.RepoName),
		zap.Int("pr_number", pr.PRNumber),
		zap.String("head_sha", pr.HeadSHA))
	return nil
}
