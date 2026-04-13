package reviews

import (
	"encoding/json"
	"fmt"
	"net/http"

	"go.uber.org/zap"
)

// Handler exposes the review store over HTTP.
type Handler struct {
	store  *Store
	logger *zap.Logger
}

// NewHandler creates a new reviews API handler.
func NewHandler(store *Store, logger *zap.Logger) *Handler {
	return &Handler{store: store, logger: logger}
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
