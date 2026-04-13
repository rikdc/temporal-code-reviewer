package types

import "time"

// PRReviewInput contains the input data for a PR review workflow
type PRReviewInput struct {
	PRNumber       int    `json:"pr_number"`
	RepoOwner      string `json:"repo_owner"`
	RepoName       string `json:"repo_name"`
	Title          string `json:"title"`
	DiffURL        string `json:"diff_url"`
	HeadBranch     string `json:"head_branch"`      // original PR's source branch
	HeadSHA        string `json:"head_sha"`         // commit SHA of the PR head — use this as Ref for file reads
	BaseBranch     string `json:"base_branch"`      // original PR's target branch
	PRAuthor       string `json:"pr_author"`        // GitHub login of the PR author
	AutoFixEnabled bool   `json:"auto_fix_enabled"` // whether to run auto-fix phases for this PR
}

// PollPRsInput is the input for the PollPRsWorkflow triggered by a Temporal Schedule.
type PollPRsInput struct {
	Repos        []string `json:"repos"`          // "owner/repo" pairs to poll
	AutoFixUsers []string `json:"auto_fix_users"` // GitHub logins allowed to receive auto-fixes
}

// AgentReviewInput contains PR metadata and fetched diff content for agent reviews
type AgentReviewInput struct {
	PRReviewInput        // Embedded PR metadata
	DiffContent   string `json:"diff_content"` // Fetched diff content
}

// AgentResult represents the output from a review agent
type AgentResult struct {
	AgentName          string    `json:"agent_name"`
	Status             string    `json:"status"` // "passed", "failed", "warning"
	Findings           []string  `json:"findings"`
	StructuredFindings []Finding `json:"structured_findings,omitempty"` // typed findings for downstream triage
	Progress           int       `json:"progress"`                     // 0-100
	Timestamp          time.Time `json:"timestamp"`
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

// Finding represents a single review finding with optional location context.
type Finding struct {
	Severity     string `json:"severity"`               // "critical", "high", "medium", "low"
	Title        string `json:"title"`                   // Brief description
	Description  string `json:"description"`             // Detailed explanation
	File         string `json:"file,omitempty"`          // relative file path
	Line         int    `json:"line,omitempty"`           // best-effort line number
	SuggestedFix string `json:"suggested_fix,omitempty"` // review agent's proposed fix
}

// TriageInput is the input for the triage classification activity.
type TriageInput struct {
	PRReviewInput PRReviewInput `json:"pr_review_input"`
	Findings      []Finding     `json:"findings"`
}

// TriageDecision is the triage agent's verdict for one finding.
type TriageDecision struct {
	Finding         Finding `json:"finding"`
	AutoFixable     bool    `json:"auto_fixable"`
	Reason          string  `json:"reason"`           // why this decision was made
	FixInstructions string  `json:"fix_instructions"` // precise instructions for fixer; empty if human-required
}

// FixFindingInput is the input for a single fixer child workflow.
type FixFindingInput struct {
	Decision   TriageDecision `json:"decision"`
	RepoOwner  string         `json:"repo_owner"`
	RepoName   string         `json:"repo_name"`
	HeadBranch string         `json:"head_branch"`
	HeadSHA    string         `json:"head_sha"` // commit SHA to use as Ref for file reads
}

// ReadFileInput is the input for the GitHub file read activity.
type ReadFileInput struct {
	RepoOwner string `json:"repo_owner"`
	RepoName  string `json:"repo_name"`
	FilePath  string `json:"file_path"`
	Ref       string `json:"ref"` // branch or commit SHA
}

// GenerateFixInput is the input for the fix generator activity.
type GenerateFixInput struct {
	Decision    TriageDecision `json:"decision"`
	FileContent string         `json:"file_content"`
}

// FixResult is the output of one fixer child workflow.
type FixResult struct {
	FindingID     string   `json:"finding_id"`      // Finding.Title used as stable ID
	Success       bool     `json:"success"`
	Diff          string   `json:"diff"`             // unified diff
	FilesChanged  []string `json:"files_changed"`
	CommitMsg     string   `json:"commit_msg"`
	FailureReason string   `json:"failure_reason,omitempty"`
}

// CoalesceInput is the input for the coalesce activity.
type CoalesceInput struct {
	FixResults []FixResult `json:"fix_results"`
	RepoOwner  string      `json:"repo_owner"`
	RepoName   string      `json:"repo_name"`
	HeadBranch string      `json:"head_branch"`
	HeadSHA    string      `json:"head_sha"` // commit SHA to branch from; avoids a getBranchSHA roundtrip
	PRNumber   int         `json:"pr_number"`
}

// CoalescedChangeset is the merged output of all fixer child workflows.
type CoalescedChangeset struct {
	Applied    []FixResult `json:"applied"`
	Conflicts  []FixResult `json:"conflicts"`
	BranchName string      `json:"branch_name"`
}

// CreatePRInput is the input for the PR creation activity.
type CreatePRInput struct {
	Changeset      CoalescedChangeset `json:"changeset"`
	OriginalPRNum  int                `json:"original_pr_num"`
	OriginalBranch string             `json:"original_branch"`
	RepoOwner      string             `json:"repo_owner"`
	RepoName       string             `json:"repo_name"`
	HumanRequired  []TriageDecision   `json:"human_required"`
}

// PostReviewInput is the input for the GitHub draft review posting activity.
type PostReviewInput struct {
	PRReviewInput PRReviewInput `json:"pr_review_input"`
	AgentResults  []AgentResult `json:"agent_results"`
	Summary       ReviewSummary `json:"summary"`
}

// CreatePRResult is the output of the PR creation activity.
type CreatePRResult struct {
	PRNumber int    `json:"pr_number"`
	PRURL    string `json:"pr_url"`
}

// PRReviewResult replaces *ReviewSummary as the workflow return type.
type PRReviewResult struct {
	Summary     ReviewSummary      `json:"summary"`
	Triage      []TriageDecision   `json:"triage"`
	Changeset   CoalescedChangeset `json:"changeset"`
	FixPRNumber int                `json:"fix_pr_number,omitempty"`
	FixPRURL    string             `json:"fix_pr_url,omitempty"`
}

// FeedbackPollerInput is the input for FeedbackPollerWorkflow.
type FeedbackPollerInput struct {
	WorkflowID     string `json:"workflow_id"`      // parent review workflow ID
	RepoOwner      string `json:"repo_owner"`
	RepoName       string `json:"repo_name"`
	PRNumber       int    `json:"pr_number"`
	GitHubReviewID int64  `json:"github_review_id"`
}

// FeedbackPollResult is the output of ActivityCheckFeedback.
type FeedbackPollResult struct {
	PRClosed         bool `json:"pr_closed"`
	DeletedComments  int  `json:"deleted_comments"`
}
