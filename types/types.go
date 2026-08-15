package types

import "time"

// PRReviewInput contains the input data for a PR review workflow
type PRReviewInput struct {
	PRNumber       int    `json:"pr_number"`
	RepoOwner      string `json:"repo_owner"`
	RepoName       string `json:"repo_name"`
	Title          string `json:"title"`
	DiffURL        string `json:"diff_url"`
	HeadBranch     string `json:"head_branch"`
	HeadSHA        string `json:"head_sha"`
	BaseBranch     string `json:"base_branch"`
	PRAuthor       string `json:"pr_author"`
	AutoFixEnabled bool   `json:"auto_fix_enabled"`
}

// PollPRsInput is the input for the PollPRsWorkflow triggered by a Temporal Schedule.
type PollPRsInput struct {
	Repos        []string `json:"repos"`
	AutoFixUsers []string `json:"auto_fix_users"`
}

// AgentReviewInput contains PR metadata and fetched diff content for agent reviews
type AgentReviewInput struct {
	PRReviewInput
	DiffContent string `json:"diff_content"`
}

// AgentResult represents the output from a review agent
type AgentResult struct {
	AgentName          string    `json:"agent_name"`
	Status             string    `json:"status"`
	Findings           []string  `json:"findings"`
	StructuredFindings []Finding `json:"structured_findings,omitempty"`
	Progress           int       `json:"progress"`
	Timestamp          time.Time `json:"timestamp"`
}

// SynthesisInput contains all agent results for the synthesis agent
type SynthesisInput struct {
	PRReviewInput PRReviewInput `json:"pr_review_input"`
	AgentResults  []AgentResult `json:"agent_results"`
}

// DiffCoverage tracks how much of the diff was actually reviewed.
type DiffCoverage struct {
	TotalFiles       int    `json:"total_files"`
	TotalDiffBytes   int    `json:"total_diff_bytes"`
	TotalDiffLines   int    `json:"total_diff_lines"`
	ReviewedFiles    int    `json:"reviewed_files"`
	ReviewedBytes    int    `json:"reviewed_bytes"`
	ReviewedLines    int    `json:"reviewed_lines"`
	OmittedFiles     int    `json:"omitted_files"`
	OmittedBytes     int    `json:"omitted_bytes"`
	OmittedLines     int    `json:"omitted_lines"`
	Truncated        bool   `json:"truncated"`
	TruncatedAtBytes int    `json:"truncated_at_bytes,omitempty"`
	TruncatedAtLines int    `json:"truncated_at_lines,omitempty"`
	OmissionReason   string `json:"omission_reason,omitempty"`
}

// ReviewSummary is the final output of the workflow
type ReviewSummary struct {
	PRNumber       int           `json:"pr_number"`
	OverallStatus  string        `json:"overall_status"`
	Recommendation string        `json:"recommendation"`
	AgentResults   []AgentResult `json:"agent_results"`
	Summary        string        `json:"summary"`
	Timestamp      time.Time     `json:"timestamp"`
	Coverage       DiffCoverage  `json:"coverage"`
}

// WorkflowEvent represents a progress event from an agent
type WorkflowEvent struct {
	WorkflowID string       `json:"workflow_id"`
	EventType  string       `json:"event_type"`
	AgentName  string       `json:"agent_name"`
	Progress   int          `json:"progress"`
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
	Severity     string `json:"severity"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	File         string `json:"file,omitempty"`
	Line         int    `json:"line,omitempty"`
	SuggestedFix string `json:"suggested_fix,omitempty"`
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
	Reason          string  `json:"reason"`
	FixInstructions string  `json:"fix_instructions"`
}

// FixFindingInput is the input for a single fixer child workflow.
type FixFindingInput struct {
	Decision   TriageDecision `json:"decision"`
	RepoOwner  string         `json:"repo_owner"`
	RepoName   string         `json:"repo_name"`
	HeadBranch string         `json:"head_branch"`
	HeadSHA    string         `json:"head_sha"`
}

// ReadFileInput is the input for the GitHub file read activity.
type ReadFileInput struct {
	RepoOwner string `json:"repo_owner"`
	RepoName  string `json:"repo_name"`
	FilePath  string `json:"file_path"`
	Ref       string `json:"ref"`
}

// GenerateFixInput is the input for the fix generator activity.
type GenerateFixInput struct {
	Decision    TriageDecision `json:"decision"`
	FileContent string         `json:"file_content"`
}

// FixResult is the output of one fixer child workflow.
type FixResult struct {
	FindingID     string   `json:"finding_id"`
	Success       bool     `json:"success"`
	Diff          string   `json:"diff"`
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
	HeadSHA    string      `json:"head_sha"`
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

// PostReviewOutput is returned by the PostReview activity.
type PostReviewOutput struct {
	GitHubReviewID int64  `json:"github_review_id"`
	ReviewBody     string `json:"review_body"`
}

// FeedbackPollerInput is the input for FeedbackPollerWorkflow.
type FeedbackPollerInput struct {
	WorkflowID     string `json:"workflow_id"`
	RepoOwner      string `json:"repo_owner"`
	RepoName       string `json:"repo_name"`
	PRNumber       int    `json:"pr_number"`
	GitHubReviewID int64  `json:"github_review_id"`
	ReviewBody     string `json:"review_body"`
}

// FeedbackPollResult is the output of ActivityCheckFeedback.
type FeedbackPollResult struct {
	PRClosed        bool `json:"pr_closed"`
	DeletedComments int  `json:"deleted_comments"`
	ReactedComments int  `json:"reacted_comments"`
	RepliedComments int  `json:"replied_comments"`
}
