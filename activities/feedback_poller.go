package activities

import (
	"context"

	"github.com/google/go-github/v68/github"
	"github.com/rikdc/temporal-code-reviewer/metrics"
	"github.com/rikdc/temporal-code-reviewer/reviews"
	"github.com/rikdc/temporal-code-reviewer/types"
	"go.uber.org/zap"
)

// FeedbackPollerActivity records raw feedback observations from GitHub.
// It does NOT interpret signals as ground truth labels.
type FeedbackPollerActivity struct {
	client      *github.Client
	metricsRepo metrics.Repository
	logger      *zap.Logger
	store       *reviews.Store // may be nil
}

func NewFeedbackPollerActivity(client *github.Client, repo metrics.Repository, logger *zap.Logger, store *reviews.Store) *FeedbackPollerActivity {
	return &FeedbackPollerActivity{client: client, metricsRepo: repo, logger: logger, store: store}
}

// CheckFeedback fetches the current state of the PR and its review comments,
// recording raw observations. It does NOT assign verdicts (tp/fp) based on
// these signals — that interpretation is deferred to a future learning system.
func (a *FeedbackPollerActivity) CheckFeedback(ctx context.Context, input types.FeedbackPollerInput) (types.FeedbackPollResult, error) {
	if a.client == nil {
		return types.FeedbackPollResult{}, nil
	}
	if input.GitHubReviewID == 0 {
		return types.FeedbackPollResult{}, nil
	}

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

	// Restore review body if user submitted via GitHub UI with empty body
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

	// Fetch comments still present on our review
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

	// Fetch all PR review comments to find replies to our comments
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
			// Comment was deleted — record as raw observation (not a verdict)
			if err := a.metricsRepo.SaveFeedback(ctx, metrics.FeedbackEvent{
				FindingID: f.ID,
				Verdict:   "observation:comment_deleted",
				Source:    "github_deleted",
			}); err != nil {
				a.logger.Warn("Failed to save observation", zap.String("finding_id", f.ID), zap.Error(err))
			} else {
				deleted++
			}
			continue
		}

		// Check reactions — record as raw observations
		reactions, _, err := a.client.Reactions.ListPullRequestCommentReactions(
			ctx, input.RepoOwner, input.RepoName, f.GitHubCommentID, nil,
		)
		if err != nil {
			a.logger.Warn("Could not fetch reactions", zap.Int64("comment_id", f.GitHubCommentID), zap.Error(err))
		} else {
			for _, r := range reactions {
				obs := reactionObservation(r.GetContent())
				if obs == "" {
					continue
				}
				if err := a.metricsRepo.SaveFeedback(ctx, metrics.FeedbackEvent{
					FindingID: f.ID,
					Verdict:   obs,
					Source:    "github_reaction",
				}); err != nil {
					a.logger.Warn("Failed to save reaction observation", zap.String("finding_id", f.ID), zap.Error(err))
				} else {
					reacted++
				}
			}
		}

		// Check for replies — record as raw observation
		if repliedIDs[f.GitHubCommentID] {
			if err := a.metricsRepo.SaveFeedback(ctx, metrics.FeedbackEvent{
				FindingID: f.ID,
				Verdict:   "observation:replied",
				Source:    "github_reply",
			}); err != nil {
				a.logger.Warn("Failed to save reply observation", zap.String("finding_id", f.ID), zap.Error(err))
			} else {
				replied++
			}
		}
	}

	if deleted+reacted+replied > 0 {
		a.logger.Info("Recorded raw feedback observations from GitHub signals",
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
			}
		}
	}

	if a.store != nil {
		a.store.MarkClosed(input.RepoOwner, input.RepoName, input.PRNumber)
	}
}

// reactionObservation maps a GitHub reaction content to a raw observation string.
// These are NOT verdicts — they are raw observations for future analysis.
func reactionObservation(content string) string {
	switch content {
	case "+1":
		return "observation:reaction_plus1"
	case "-1":
		return "observation:reaction_minus1"
	case "heart":
		return "observation:reaction_heart"
	case "hooray":
		return "observation:reaction_hooray"
	case "rocket":
		return "observation:reaction_rocket"
	case "confused":
		return "observation:reaction_confused"
	default:
		return ""
	}
}
