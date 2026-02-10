package types

import "time"

// PRReviewInput contains the input data for a PR review workflow
type PRReviewInput struct {
	PRNumber  int    `json:"pr_number"`
	RepoOwner string `json:"repo_owner"`
	RepoName  string `json:"repo_name"`
	Title     string `json:"title"`
	DiffURL   string `json:"diff_url"`
}

// AgentReviewInput contains PR metadata and fetched diff content for agent reviews
type AgentReviewInput struct {
	PRReviewInput        // Embedded PR metadata
	DiffContent   string `json:"diff_content"` // Fetched diff content
}

// AgentResult represents the output from a review agent
type AgentResult struct {
	AgentName string    `json:"agent_name"`
	Status    string    `json:"status"` // "passed", "failed", "warning"
	Findings  []string  `json:"findings"`
	Progress  int       `json:"progress"` // 0-100
	Timestamp time.Time `json:"timestamp"`
}

// SynthesisInput contains all agent results for the synthesis agent
type SynthesisInput struct {
	PRReviewInput PRReviewInput `json:"pr_review_input"`
	AgentResults  []AgentResult `json:"agent_results"`
}

// ReviewSummary is the final output of the workflow
type ReviewSummary struct {
	PRNumber       int           `json:"pr_number"`
	OverallStatus  string        `json:"overall_status"` // "approved", "needs_changes", "blocked"
	Recommendation string        `json:"recommendation"`
	AgentResults   []AgentResult `json:"agent_results"`
	Summary        string        `json:"summary"`
	Timestamp      time.Time     `json:"timestamp"`
}

// WorkflowEvent represents a progress event from an agent
type WorkflowEvent struct {
	WorkflowID string       `json:"workflow_id"`
	EventType  string       `json:"event_type"` // "agent_started", "agent_progress", "agent_completed", "agent_failed"
	AgentName  string       `json:"agent_name"`
	Progress   int          `json:"progress"` // 0-100
	Result     *AgentResult `json:"result,omitempty"`
	Error      string       `json:"error,omitempty"`
	Timestamp  time.Time    `json:"timestamp"`
}

// Agent status constants
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusPassed    = "passed"
	StatusFailed    = "failed"
	StatusWarning   = "warning"
	StatusCompleted = "completed"
)

// Event type constants
const (
	EventAgentStarted   = "agent_started"
	EventAgentProgress  = "agent_progress"
	EventAgentCompleted = "agent_completed"
	EventAgentFailed    = "agent_failed"
)
