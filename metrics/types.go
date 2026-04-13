package metrics

import "time"

// Verdict constants for FeedbackEvent.Verdict.
const (
	VerdictTP      = "tp"
	VerdictFP      = "fp"
	VerdictIgnored = "ignored"
)

// ReviewRun records one complete PR review workflow execution.
type ReviewRun struct {
	ID             string    // Temporal workflow ID
	PRNumber       int
	RepoOwner      string
	RepoName       string
	HeadSHA        string
	GitHubReviewID int64     // 0 until the draft review is posted
	CreatedAt      time.Time
}

// AgentRun records the execution of one agent within a review.
type AgentRun struct {
	ID              string
	ReviewRunID     string // Temporal workflow ID
	AgentName       string
	Status          string // "passed", "warning", "failed"
	Model           string
	InputTokens     int
	OutputTokens    int
	LatencyMS       int64
	FindingsCount   int
	PromptVersionID string // empty when falling back to disk
	CreatedAt       time.Time
}

// FindingRecord records a single finding posted as a GitHub comment.
type FindingRecord struct {
	ID              string
	AgentRunID      string
	Severity        string
	Title           string
	FilePath        string
	LineNumber      int
	GitHubCommentID int64 // 0 for body-only findings
	CreatedAt       time.Time
}

// FeedbackEvent records a verdict on a single finding.
type FeedbackEvent struct {
	ID        string
	FindingID string
	Verdict   string // "tp", "fp", "ignored"
	Source    string // "manual", "github_deleted"
	CreatedAt time.Time
}

// PromptVersion stores a versioned prompt for one agent.
type PromptVersion struct {
	ID        string
	AgentName string
	Label     string // human-readable name, e.g. "v1", "stricter-style-v2"
	Content   string
	Disabled  bool
	CreatedAt time.Time
}

// AgentMetrics is a rolled-up summary for one agent over a time window.
type AgentMetrics struct {
	AgentName       string
	ReviewCount     int
	FindingsTotal   int
	FalsePositives  int
	TruePositives   int
	FPRate          float64
	AvgLatencyMS    float64
	AvgInputTokens  float64
	AvgOutputTokens float64
}

// PromptVersionMetrics is a rolled-up summary for one prompt version.
type PromptVersionMetrics struct {
	PromptVersionID string
	AgentName       string
	Label           string
	ReviewCount     int
	FindingsTotal   int
	FalsePositives  int
	TruePositives   int
	FPRate          float64
}
