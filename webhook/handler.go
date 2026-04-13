package webhook

import (
	"encoding/json"
	"fmt"
	"net/http"

	enumspb "go.temporal.io/api/enums/v1"
	"github.com/rikdc/temporal-code-reviewer/types"
	"github.com/rikdc/temporal-code-reviewer/workflows"
	"go.temporal.io/sdk/client"
	"go.uber.org/zap"
)

// Handler processes GitHub webhook events
type Handler struct {
	temporalClient client.Client
	logger         *zap.Logger
	autoFixUsers   map[string]bool // GitHub logins that receive auto-fix PRs
}

// NewHandler creates a new webhook handler
func NewHandler(temporalClient client.Client, logger *zap.Logger, autoFixUsers []string) *Handler {
	allowed := make(map[string]bool, len(autoFixUsers))
	for _, u := range autoFixUsers {
		allowed[u] = true
	}
	return &Handler{
		temporalClient: temporalClient,
		logger:         logger,
		autoFixUsers:   allowed,
	}
}

// GitHubPRPayload represents the GitHub PR webhook payload
type GitHubPRPayload struct {
	Action      string      `json:"action"`
	Number      int         `json:"number"`
	Repository  Repository  `json:"repository"`
	PullRequest PullRequest `json:"pull_request"`
	Sender      Sender      `json:"sender"`
}

type Sender struct {
	Login string `json:"login"`
}

type Repository struct {
	Name  string `json:"name"`
	Owner Owner  `json:"owner"`
}

type Owner struct {
	Login string `json:"login"`
}

type PullRequest struct {
	Number  int        `json:"number"`
	Title   string     `json:"title"`
	DiffURL string     `json:"diff_url"`
	Head    BranchRef  `json:"head"`
	Base    BranchRef  `json:"base"`
}

type BranchRef struct {
	Ref string `json:"ref"`
	SHA string `json:"sha"`
}

// HandlePR processes PR webhook events
func (h *Handler) HandlePR(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var payload GitHubPRPayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		h.logger.Error("Failed to decode webhook payload", zap.Error(err))
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	// Only process "opened" events for this demo
	if payload.Action != "opened" {
		h.logger.Info("Ignoring non-opened PR event", zap.String("action", payload.Action))
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"message": fmt.Sprintf("Ignored action: %s", payload.Action),
		})
		return
	}

	// Short SHA makes the ID human-readable in the Temporal UI and ensures
	// it matches the ID the poller would generate for the same commit, so
	// Temporal's reuse policy provides dedup across both trigger sources.
	headSHA := payload.PullRequest.Head.SHA
	shortSHA := headSHA
	if len(shortSHA) > 8 {
		shortSHA = shortSHA[:8]
	}
	workflowID := fmt.Sprintf("pr-review/%s/%s/%d/%s",
		payload.Repository.Owner.Login,
		payload.Repository.Name,
		payload.PullRequest.Number,
		shortSHA)

	h.logger.Info("Starting PR review workflow",
		zap.String("workflow_id", workflowID),
		zap.Int("pr_number", payload.PullRequest.Number))

	// Prepare workflow input
	input := types.PRReviewInput{
		PRNumber:       payload.PullRequest.Number,
		RepoOwner:      payload.Repository.Owner.Login,
		RepoName:       payload.Repository.Name,
		Title:          payload.PullRequest.Title,
		DiffURL:        payload.PullRequest.DiffURL,
		HeadBranch:     payload.PullRequest.Head.Ref,
		HeadSHA:        payload.PullRequest.Head.SHA,
		BaseBranch:     payload.PullRequest.Base.Ref,
		PRAuthor:       payload.Sender.Login,
		AutoFixEnabled: h.autoFixUsers[payload.Sender.Login],
	}

	// Start Temporal workflow.
	// ALLOW_DUPLICATE_FAILED_ONLY prevents re-review of a PR that was already
	// successfully reviewed at this SHA — matching the poller's policy so both
	// trigger sources share the same dedup guarantee.
	options := client.StartWorkflowOptions{
		ID:                       workflowID,
		TaskQueue:                "pr-review-queue",
		WorkflowIDReusePolicy:    enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE_FAILED_ONLY,
	}

	workflowRun, err := h.temporalClient.ExecuteWorkflow(r.Context(), options, workflows.PRReviewWorkflow, input)
	if err != nil {
		h.logger.Error("Failed to start workflow", zap.Error(err))
		http.Error(w, "Failed to start workflow", http.StatusInternalServerError)
		return
	}

	// Return response with dashboard URL (use workflow ID, not run ID)
	dashboardURL := fmt.Sprintf("http://localhost:8081/dashboard?workflowId=%s", workflowID)

	response := map[string]string{
		"workflow_id":   workflowID,
		"run_id":        workflowRun.GetRunID(),
		"dashboard_url": dashboardURL,
		"status":        "started",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)

	h.logger.Info("PR review workflow started",
		zap.String("workflow_id", workflowID),
		zap.String("dashboard_url", dashboardURL))
}
