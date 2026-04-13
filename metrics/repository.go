package metrics

import (
	"context"
	"time"
)

// Repository is the storage interface for all metrics data.
// Implementations may use SQLite, PostgreSQL, DynamoDB, etc.
type Repository interface {
	// Prompt versions
	SeedPrompt(ctx context.Context, agentName, label, content string) error
	GetActivePromptVersions(ctx context.Context, agentName string) ([]PromptVersion, error)
	DisablePromptVersion(ctx context.Context, id string) error
	AddPromptVersion(ctx context.Context, pv PromptVersion) error
	ListPromptVersions(ctx context.Context, agentName string) ([]PromptVersion, error)

	// Review runs
	SaveReviewRun(ctx context.Context, r ReviewRun) error
	SetGitHubReviewID(ctx context.Context, workflowID string, reviewID int64) error
	HasReviewedAtSHA(ctx context.Context, repoOwner, repoName string, prNumber int, headSHA string) (bool, error)

	// Explicit skips — recorded when a user discards a review so the poller
	// does not re-review the same PR+SHA.
	RecordSkip(ctx context.Context, repoOwner, repoName string, prNumber int, headSHA string) error

	// Agent runs
	SaveAgentRun(ctx context.Context, r AgentRun) error
	GetAgentRunID(ctx context.Context, workflowID, agentName string) (string, bool, error)

	// Findings
	SaveFindings(ctx context.Context, findings []FindingRecord) error
	GetFindingsByReviewRun(ctx context.Context, workflowID string) ([]FindingRecord, error)
	GetFindingByCommentID(ctx context.Context, commentID int64) (FindingRecord, bool, error)

	// Feedback
	SaveFeedback(ctx context.Context, f FeedbackEvent) error

	// Metrics queries
	GetAgentMetrics(ctx context.Context, agentName string, since time.Time) (AgentMetrics, error)
	GetPromptVersionMetrics(ctx context.Context, promptVersionID string) (PromptVersionMetrics, error)
	ListAgentMetrics(ctx context.Context, since time.Time) ([]AgentMetrics, error)
}
