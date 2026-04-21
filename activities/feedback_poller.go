package activities

import (
	"context"

	"github.com/google/go-github/v68/github"
	"github.com/rikdc/temporal-code-reviewer/metrics"
	"github.com/rikdc/temporal-code-reviewer/reviews"
	"github.com/rikdc/temporal-code-reviewer/types"
	"go.uber.org/zap"
)

// FeedbackPollerActivity checks which of our review comments still exist on
// GitHub and records deleted ones as implicit false-positive feedback.
type FeedbackPollerActivity struct {
	client      *github.Client
	metricsRepo metrics.Repository
	logger      *zap.Logger
	store       *reviews.Store // may be nil
}

// NewFeedbackPollerActivity creates a new FeedbackPollerActivity.
// store may be nil if dashboard state tracking is not required.
func NewFeedbackPollerActivity(client *github.Client, repo metrics.Repository, logger *zap.Logger, store *reviews.Store) *FeedbackPollerActivity {
	return &FeedbackPollerActivity{client: client, metricsRepo: repo, logger: logger, store: store}
}

// CheckFeedback fetches the current state of the PR and its review comments,
// then records implicit feedback from three signals:
//
//   - Deleted comments → fp (github_deleted)
//   - Reactions (+1/heart/hooray/rocket → tp; -1/confused → fp) (github_reaction)
//   - Replies to our comments → tp (github_reply)
//
// Returns PRClosed=true when the PR is merged or closed, signalling the
// polling loop to stop. Uses INSERT OR IGNORE so repeated polls are idempotent.
func (a *FeedbackPollerActivity) CheckFeedback(ctx context.Context, input types.FeedbackPollerInput) (types.FeedbackPollResult, error) {
	pr, _, err := a.client.PullRequests.Get(ctx, input.RepoOwner, input.RepoName, input.PRNumber)
	if err != nil {
		a.logger.Warn("Could not fetch PR state for feedback poll",
			zap.Int("pr_number", input.PRNumber), zap.Error(err))
		return types.FeedbackPollResult{}, nil
	}
	if pr.GetState() == "closed" {
		a.cleanupClosedPR(ctx, input)
		return types.FeedbackPollResult{PRClosed: true}, nil
	}

	if input.GitHubReviewID == 0 {
		return types.FeedbackPollResult{}, nil
	}

	// Restore the review body if the user has submitted the pending review via
	// the GitHub UI. The GitHub "Finish your review" dialog always submits with
	// an empty body, overwriting the body we set when creating the review.
	// PUT /reviews/{review_id} works on submitted reviews, so we use it to
	// restore the body on the first poll after submission.
	if input.ReviewBody != "" {
		review, _, err := a.client.PullRequests.GetReview(ctx, input.RepoOwner, input.RepoName, input.PRNumber, input.GitHubReviewID)
		if err != nil {
			a.logger.Warn("Could not fetch review state for body restore check",
				zap.Int64("review_id", input.GitHubReviewID), zap.Error(err))
		} else if review.GetState() != "PENDING" && review.GetBody() == "" {
			if _, _, err := a.client.PullRequests.UpdateReview(ctx, input.RepoOwner, input.RepoName, input.PRNumber, input.GitHubReviewID, input.ReviewBody); err != nil {
				a.logger.Warn("Could not restore review body after submission",
					zap.Int64("review_id", input.GitHubReviewID), zap.Error(err))
			} else {
				a.logger.Info("Restored review body after user submission",
					zap.Int("pr_number", input.PRNumber),
					zap.Int64("review_id", input.GitHubReviewID))
			}
		}
	}

	// Fetch comments still present on our review.
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

	// Fetch all PR review comments to find replies to our comments.
	allComments, _, err := a.client.PullRequests.ListComments(
		ctx, input.RepoOwner, input.RepoName, input.PRNumber, nil,
	)
	if err != nil {
		a.logger.Warn("Could not list all PR comments for reply detection", zap.Error(err))
	}
	repliedIDs := make(map[int64]bool, len(allComments))
	for _, c := range allComments {
		if id := c.GetInReplyTo(); id != 0 {
			repliedIDs[id] = true
		}
	}

	findings, err := a.metricsRepo.GetFindingsByReviewRun(ctx, input.WorkflowID)
	if err != nil {
		a.logger.Warn("Could not load findings for feedback poll", zap.Error(err))
		return types.FeedbackPollResult{}, nil
	}

	var deleted, reacted, replied int

	for _, f := range findings {
		if f.GitHubCommentID == 0 {
			continue
		}

		if !liveIDs[f.GitHubCommentID] {
			// Comment was deleted — implicit false positive.
			if err := a.metricsRepo.SaveFeedback(ctx, metrics.FeedbackEvent{
				FindingID: f.ID,
				Verdict:   metrics.VerdictFP,
				Source:    "github_deleted",
			}); err != nil {
				a.logger.Warn("Failed to save implicit feedback", zap.String("finding_id", f.ID), zap.Error(err))
			} else {
				deleted++
			}
			continue
		}

		// Comment is still live — check reactions.
		reactions, _, err := a.client.Reactions.ListPullRequestCommentReactions(
			ctx, input.RepoOwner, input.RepoName, f.GitHubCommentID, nil,
		)
		if err != nil {
			a.logger.Warn("Could not fetch reactions", zap.Int64("comment_id", f.GitHubCommentID), zap.Error(err))
		} else {
			for _, r := range reactions {
				verdict := reactionVerdict(r.GetContent())
				if verdict == "" {
					continue
				}
				if err := a.metricsRepo.SaveFeedback(ctx, metrics.FeedbackEvent{
					FindingID: f.ID,
					Verdict:   verdict,
					Source:    "github_reaction",
				}); err != nil {
					a.logger.Warn("Failed to save reaction feedback", zap.String("finding_id", f.ID), zap.Error(err))
				} else {
					reacted++
				}
				break // first meaningful reaction wins; INSERT OR IGNORE handles subsequent polls
			}
		}

		// Check for replies.
		if repliedIDs[f.GitHubCommentID] {
			if err := a.metricsRepo.SaveFeedback(ctx, metrics.FeedbackEvent{
				FindingID: f.ID,
				Verdict:   metrics.VerdictTP,
				Source:    "github_reply",
			}); err != nil {
				a.logger.Warn("Failed to save reply feedback", zap.String("finding_id", f.ID), zap.Error(err))
			} else {
				replied++
			}
		}
	}

	if deleted+reacted+replied > 0 {
		a.logger.Info("Recorded implicit feedback from GitHub signals",
			zap.Int("pr_number", input.PRNumber),
			zap.Int("deleted", deleted),
			zap.Int("reacted", reacted),
			zap.Int("replied", replied))
	}

	return types.FeedbackPollResult{
		DeletedComments: deleted,
		ReactedComments: reacted,
		RepliedComments: replied,
	}, nil
}

// cleanupClosedPR deletes the pending GitHub review (if any) and marks the
// review record as closed in the dashboard store.
func (a *FeedbackPollerActivity) cleanupClosedPR(ctx context.Context, input types.FeedbackPollerInput) {
	if input.GitHubReviewID != 0 {
		review, _, err := a.client.PullRequests.GetReview(ctx, input.RepoOwner, input.RepoName, input.PRNumber, input.GitHubReviewID)
		if err != nil {
			a.logger.Warn("Could not fetch review for closed-PR cleanup",
				zap.Int("pr_number", input.PRNumber),
				zap.Int64("review_id", input.GitHubReviewID),
				zap.Error(err))
		} else if review.GetState() == "PENDING" {
			if _, _, err := a.client.PullRequests.DeletePendingReview(ctx, input.RepoOwner, input.RepoName, input.PRNumber, input.GitHubReviewID); err != nil {
				a.logger.Warn("Could not delete pending review on PR close",
					zap.Int("pr_number", input.PRNumber),
					zap.Int64("review_id", input.GitHubReviewID),
					zap.Error(err))
			} else {
				a.logger.Info("Deleted pending review on PR close",
					zap.Int("pr_number", input.PRNumber),
					zap.Int64("review_id", input.GitHubReviewID))
			}
		}
	}

	if a.store != nil {
		a.store.MarkClosed(input.RepoOwner, input.RepoName, input.PRNumber)
	}
}

// reactionVerdict maps a GitHub reaction content string to a feedback verdict.
// +1/heart/hooray/rocket are treated as true-positive signals (user agrees with
// the finding); -1/confused as false-positive signals (user disagrees).
// All other reactions are ignored.
func reactionVerdict(content string) string {
	switch content {
	case "+1", "heart", "hooray", "rocket":
		return metrics.VerdictTP
	case "-1", "confused":
		return metrics.VerdictFP
	default:
		return ""
	}
}
