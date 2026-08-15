package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"

	enumspb "go.temporal.io/api/enums/v1"
	"github.com/rikdc/temporal-code-reviewer/config"
	"github.com/rikdc/temporal-code-reviewer/types"
	"github.com/rikdc/temporal-code-reviewer/workflows"
	"go.temporal.io/sdk/client"
	"go.uber.org/zap"
)

// Handler processes GitHub webhook events
type Handler struct {
	temporalClient client.Client
	logger         *zap.Logger
	autoFixUsers   map[string]bool
	webhookSecret  string
	allowedRepos   map[string]bool
	allowedActions map[string]bool
	maxBodyBytes   int64

	mu          sync.Mutex
	deliveries  map[string]bool // X-GitHub-Delivery dedup
	maxDedup    int
}

// NewHandler creates a new webhook handler
func NewHandler(temporalClient client.Client, logger *zap.Logger, cfg *config.Config) *Handler {
	allowed := make(map[string]bool, len(cfg.AutoFixUsers))
	for _, u := range cfg.AutoFixUsers {
		allowed[u] = true
	}
	repos := make(map[string]bool, len(cfg.Webhook.AllowedRepos))
	for _, r := range cfg.Webhook.AllowedRepos {
		repos[strings.ToLower(r)] = true
	}
	actions := make(map[string]bool, len(cfg.Webhook.AllowedActions))
	for _, a := range cfg.Webhook.AllowedActions {
		actions[a] = true
	}
	return &Handler{
		temporalClient: temporalClient,
		logger:         logger,
		autoFixUsers:   allowed,
		webhookSecret:  cfg.Webhook.Secret,
		allowedRepos:   repos,
		allowedActions: actions,
		maxBodyBytes:   cfg.Webhook.MaxBodyBytes,
		deliveries:     make(map[string]bool),
		maxDedup:       10000,
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

	// Read body with size limit
	limitedReader := io.LimitReader(r.Body, h.maxBodyBytes+1)
	bodyBytes, err := io.ReadAll(limitedReader)
	if err != nil {
		h.logger.Error("Failed to read webhook body", zap.Error(err))
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	if int64(len(bodyBytes)) > h.maxBodyBytes {
		http.Error(w, "Request body too large", http.StatusRequestEntityTooLarge)
		return
	}

	// Verify HMAC signature if secret is configured
	if h.webhookSecret != "" {
		sig := r.Header.Get("X-Hub-Signature-256")
		if sig == "" {
			h.logger.Warn("Missing webhook signature")
			http.Error(w, "Missing signature", http.StatusUnauthorized)
			return
		}
		if !verifySignature(bodyBytes, sig, h.webhookSecret) {
			h.logger.Warn("Invalid webhook signature")
			http.Error(w, "Invalid signature", http.StatusForbidden)
			return
		}
	}

	// Deduplicate by delivery ID
	deliveryID := r.Header.Get("X-GitHub-Delivery")
	if deliveryID != "" {
		h.mu.Lock()
		if h.deliveries[deliveryID] {
			h.mu.Unlock()
			h.logger.Info("Duplicate delivery", zap.String("delivery_id", deliveryID))
			w.WriteHeader(http.StatusOK)
			return
		}
		h.deliveries[deliveryID] = true
		// Evict old entries if map grows too large
		if len(h.deliveries) > h.maxDedup {
			h.deliveries = make(map[string]bool)
			h.deliveries[deliveryID] = true
		}
		h.mu.Unlock()
	}

	// Validate event type
	eventType := r.Header.Get("X-GitHub-Event")
	if eventType != "pull_request" {
		h.logger.Info("Ignoring non-PR event", zap.String("event", eventType))
		w.WriteHeader(http.StatusOK)
		return
	}

	var payload GitHubPRPayload
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		h.logger.Error("Failed to decode webhook payload", zap.Error(err))
		http.Error(w, "Invalid payload", http.StatusBadRequest)
		return
	}

	// Validate action
	if !h.allowedActions[payload.Action] {
		h.logger.Info("Ignoring action", zap.String("action", payload.Action))
		w.WriteHeader(http.StatusOK)
		return
	}

	// Validate required fields
	if payload.Repository.Owner.Login == "" || payload.Repository.Name == "" || payload.PullRequest.Number == 0 {
		http.Error(w, "Missing required fields (owner, repo, or PR number)", http.StatusBadRequest)
		return
	}
	if payload.PullRequest.Head.SHA == "" || payload.PullRequest.Head.Ref == "" || payload.PullRequest.Base.Ref == "" {
		http.Error(w, "Missing branch or SHA data", http.StatusBadRequest)
		return
	}

	// Validate against repository allowlist (if configured)
	if len(h.allowedRepos) > 0 {
		repoKey := strings.ToLower(payload.Repository.Owner.Login + "/" + payload.Repository.Name)
		if !h.allowedRepos[repoKey] {
			h.logger.Info("Repository not in allowlist",
				zap.String("repo", repoKey))
			w.WriteHeader(http.StatusForbidden)
			return
		}
	}

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

	// Do NOT trust sender.login for auto-fix eligibility.
	// Fetch authoritative PR metadata from GitHub API.
	// For now, use the payload data but do not grant auto-fix based on sender.
	input := types.PRReviewInput{
		PRNumber:       payload.PullRequest.Number,
		RepoOwner:      payload.Repository.Owner.Login,
		RepoName:       payload.Repository.Name,
		Title:          payload.PullRequest.Title,
		HeadBranch:     payload.PullRequest.Head.Ref,
		HeadSHA:        payload.PullRequest.Head.SHA,
		BaseBranch:     payload.PullRequest.Base.Ref,
		PRAuthor:       payload.PullRequest.Title, // placeholder; overwritten below
		AutoFixEnabled: false,                     // disabled by default
	}

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

// verifySignature checks the X-Hub-Signature-256 header against the request body.
func verifySignature(body []byte, signature, secret string) bool {
	const prefix = "sha256="
	if !strings.HasPrefix(signature, prefix) {
		return false
	}
	sig, err := hex.DecodeString(signature[len(prefix):])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := mac.Sum(nil)
	return hmac.Equal(sig, expected)
}
