package dashboard

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"

	"github.com/rikdc/temporal-code-reviewer/events"
	"go.uber.org/zap"
)

// Server handles dashboard HTTP requests
type Server struct {
	eventBus events.Subscriber
	logger   *zap.Logger
	tmpl     *template.Template
}

// NewServer creates a new dashboard server
func NewServer(eventBus events.Subscriber, logger *zap.Logger) *Server {
	tmpl := template.Must(template.ParseFiles("dashboard/templates/index.html"))
	return &Server{
		eventBus: eventBus,
		logger:   logger,
		tmpl:     tmpl,
	}
}

// Start starts the dashboard HTTP server
func (s *Server) Start(addr string) error {
	mux := http.NewServeMux()

	// Serve static files
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("dashboard/static"))))

	// Dashboard page
	mux.HandleFunc("/dashboard", s.handleDashboard)

	// SSE endpoint for real-time updates
	mux.HandleFunc("/api/events", s.handleSSE)

	s.logger.Info("Dashboard server starting", zap.String("address", addr))
	return http.ListenAndServe(addr, mux)
}

// handleDashboard serves the dashboard HTML page
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	workflowID := r.URL.Query().Get("workflowId")
	if workflowID == "" {
		http.Error(w, "Missing workflowId parameter", http.StatusBadRequest)
		return
	}

	data := map[string]interface{}{
		"WorkflowID": workflowID,
	}

	if err := s.tmpl.Execute(w, data); err != nil {
		s.logger.Error("Failed to render template", zap.Error(err))
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
	}
}

// handleSSE streams workflow events via Server-Sent Events
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	workflowID := r.URL.Query().Get("workflowId")
	if workflowID == "" {
		http.Error(w, "Missing workflowId parameter", http.StatusBadRequest)
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Subscribe to events for this workflow
	eventChan := s.eventBus.Subscribe(workflowID)
	defer s.eventBus.Unsubscribe(workflowID, eventChan)

	s.logger.Info("SSE connection established", zap.String("workflow_id", workflowID))

	// Stream events
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	for {
		select {
		case event := <-eventChan:
			data, err := json.Marshal(event)
			if err != nil {
				s.logger.Error("Failed to marshal event", zap.Error(err))
				continue
			}

			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()

		case <-r.Context().Done():
			s.logger.Info("SSE connection closed", zap.String("workflow_id", workflowID))
			return
		}
	}
}
