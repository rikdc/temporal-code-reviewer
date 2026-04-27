package reviews

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/go-github/v68/github"
	"go.uber.org/zap"
)

// Handler exposes the review store over HTTP.
type Handler struct {
	store    *Store
	ghClient *github.Client
	logger   *zap.Logger
}

// NewHandler creates a new reviews API handler.
func NewHandler(store *Store, ghClient *github.Client, logger *zap.Logger) *Handler {
	return &Handler{store: store, ghClient: ghClient, logger: logger}
}

// HandleList returns all review records as a JSON array.
// GET /api/reviews
func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	records := h.store.List()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	if err := json.NewEncoder(w).Encode(records); err != nil {
		h.logger.Error("Failed to encode reviews", zap.Error(err))
	}
}

// HandleStream pushes new review records to the client via Server-Sent Events.
// The stream stays open until the client disconnects.
// GET /api/reviews/stream
func (h *Handler) HandleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	ch := h.store.Subscribe()
	defer h.store.Unsubscribe(ch)

	// Flush headers immediately so clients don't hang waiting for the first event.
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	h.logger.Info("Review SSE connection established",
		zap.String("remote_addr", r.RemoteAddr))

	for {
		select {
		case rec := <-ch:
			data, err := json.Marshal(rec)
			if err != nil {
				h.logger.Error("Failed to marshal review record", zap.Error(err))
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()

		case <-r.Context().Done():
			h.logger.Info("Review SSE connection closed",
				zap.String("remote_addr", r.RemoteAddr))
			return
		}
	}
}

// HandleSubmit programmatically submits a PENDING GitHub review, preserving the
// review body that GitHub's UI would otherwise discard.
// POST /api/reviews/submit
// Body: {"repo_owner":"...","repo_name":"...","pr_number":123}
func (h *Handler) HandleSubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.ghClient == nil {
		http.Error(w, "GitHub client not configured", http.StatusServiceUnavailable)
		return
	}

	var body struct {
		RepoOwner string `json:"repo_owner"`
		RepoName  string `json:"repo_name"`
		PRNumber  int    `json:"pr_number"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if body.RepoOwner == "" || body.RepoName == "" || body.PRNumber == 0 {
		http.Error(w, "repo_owner, repo_name, and pr_number are required", http.StatusBadRequest)
		return
	}

	rec := h.store.FindPendingByPR(body.RepoOwner, body.RepoName, body.PRNumber)
	if rec == nil {
		http.Error(w, "no pending review found for this PR", http.StatusNotFound)
		return
	}

	if rec.GitHubReviewID == 0 {
		http.Error(w, "review has no GitHub review ID", http.StatusUnprocessableEntity)
		return
	}

	_, _, err := h.ghClient.PullRequests.SubmitReview(
		r.Context(),
		body.RepoOwner,
		body.RepoName,
		body.PRNumber,
		rec.GitHubReviewID,
		&github.PullRequestReviewRequest{
			Body:  github.String(rec.ReviewBody),
			Event: github.String("COMMENT"),
		},
	)
	if err != nil {
		h.logger.Error("Failed to submit review via GitHub API",
			zap.Int("pr_number", body.PRNumber),
			zap.Int64("review_id", rec.GitHubReviewID),
			zap.Error(err))
		http.Error(w, fmt.Sprintf("GitHub API error: %v", err), http.StatusBadGateway)
		return
	}

	h.store.MarkSubmitted(body.RepoOwner, body.RepoName, body.PRNumber)

	h.logger.Info("Review submitted programmatically",
		zap.String("repo", body.RepoOwner+"/"+body.RepoName),
		zap.Int("pr_number", body.PRNumber),
		zap.Int64("review_id", rec.GitHubReviewID))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"review_id": rec.GitHubReviewID,
		"state":     "submitted",
	})
}
