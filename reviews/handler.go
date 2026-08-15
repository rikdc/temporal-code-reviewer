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

func NewHandler(store *Store, ghClient *github.Client, logger *zap.Logger) *Handler {
	return &Handler{store: store, ghClient: ghClient, logger: logger}
}

// ServeHTTP routes requests based on method and path.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case path == "/api/reviews/stream":
		h.HandleStream(w, r)
	case path == "/api/reviews/submit":
		h.HandleSubmit(w, r)
	case path == "/api/reviews":
		switch r.Method {
		case http.MethodGet:
			h.HandleList(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func (h *Handler) HandleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	records := h.store.List()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(records); err != nil {
		h.logger.Error("Failed to encode reviews", zap.Error(err))
	}
}

func (h *Handler) HandleStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := h.store.Subscribe()
	defer h.store.Unsubscribe(ch)

	w.WriteHeader(http.StatusOK)
	flusher.Flush()

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
			return
		}
	}
}

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
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"review_id": rec.GitHubReviewID,
		"state":     "submitted",
	})
}

// Ensure Handler satisfies http.Handler
var _ http.Handler = (*Handler)(nil)
