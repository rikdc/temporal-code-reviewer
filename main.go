package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/google/go-github/v68/github"
	"github.com/rikdc/temporal-code-reviewer/activities"
	"github.com/rikdc/temporal-code-reviewer/config"
	"github.com/rikdc/temporal-code-reviewer/dashboard"
	"github.com/rikdc/temporal-code-reviewer/events"
	"github.com/rikdc/temporal-code-reviewer/llm"
	"github.com/rikdc/temporal-code-reviewer/metrics"
	metricsqlite "github.com/rikdc/temporal-code-reviewer/metrics/sqlite"
	"github.com/rikdc/temporal-code-reviewer/middleware"
	"github.com/rikdc/temporal-code-reviewer/reviews"
	"github.com/rikdc/temporal-code-reviewer/types"
	"github.com/rikdc/temporal-code-reviewer/webhook"
	"github.com/rikdc/temporal-code-reviewer/workflows"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
	"go.uber.org/zap"
)

func main() {
	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Sync()

	logger.Info("Starting Temporal Code Reviewer")

	cfg, err := config.Load("config.yaml")
	if err != nil {
		logger.Fatal("Failed to load config", zap.Error(err))
	}
	logger.Info("Configuration loaded successfully",
		zap.Bool("api_key_set", cfg.OpenRouter.APIKey != ""),
		zap.Bool("webhook_enabled", cfg.Webhook.Enabled),
		zap.Bool("admin_auth", cfg.Admin.Token != ""))

	llmClient := llm.NewClient(&cfg.OpenRouter, logger)
	promptLoader := llm.NewPromptLoader("prompts")

	// Metrics store
	homeDir, err := os.UserHomeDir()
	if err != nil {
		logger.Fatal("Failed to get home directory", zap.Error(err))
	}
	metricsDBPath := filepath.Join(homeDir, ".config", "temporal-reviewer", "metrics.db")
	metricsStore, err := metricsqlite.Open(metricsDBPath)
	if err != nil {
		logger.Fatal("Failed to open metrics database", zap.String("path", metricsDBPath), zap.Error(err))
	}
	defer metricsStore.Close()
	logger.Info("Metrics database opened", zap.String("path", metricsDBPath))

	// Seed prompts
	type agentSeed struct{ name, file string }
	seeds := []agentSeed{
		{"Security", cfg.Agents.Security.PromptFile},
		{"Style", cfg.Agents.Style.PromptFile},
		{"Logic", cfg.Agents.Logic.PromptFile},
		{"Documentation", cfg.Agents.Documentation.PromptFile},
		{"Triage", cfg.Agents.Triage.PromptFile},
	}
	for _, s := range seeds {
		content, err := promptLoader.Load(s.file)
		if err != nil {
			logger.Warn("Could not read prompt file for seeding", zap.String("agent", s.name), zap.Error(err))
			continue
		}
		if err := metricsStore.SeedPrompt(context.Background(), s.name, "v1", content); err != nil {
			logger.Warn("Could not seed prompt version", zap.String("agent", s.name), zap.Error(err))
		}
	}

	promptRegistry := metrics.NewPromptRegistry(metricsStore, promptLoader)
	eventBus := events.NewEventBus()
	reviewStore := reviews.NewStoreWithPersistence(metricsStore)

	// GitHub client
	githubToken := os.Getenv("GITHUB_TOKEN")
	var ghClient *github.Client
	if githubToken != "" {
		ghClient = github.NewClient(nil).WithAuthToken(githubToken)
	} else {
		logger.Warn("GITHUB_TOKEN not set — GitHub integration disabled")
	}

	// Connect to Temporal
	temporalAddress := os.Getenv("TEMPORAL_ADDRESS")
	if temporalAddress == "" {
		temporalAddress = "localhost:7233"
	}
	temporalNamespace := cfg.Temporal.Namespace
	if temporalNamespace == "" {
		temporalNamespace = "default"
	}

	logger.Info("Connecting to Temporal",
		zap.String("address", temporalAddress),
		zap.String("namespace", temporalNamespace))

	temporalClient, err := client.Dial(client.Options{
		HostPort:  temporalAddress,
		Namespace: temporalNamespace,
	})
	if err != nil {
		logger.Fatal("Failed to connect to Temporal", zap.Error(err))
	}
	defer temporalClient.Close()

	// Create Temporal worker
	w := worker.New(temporalClient, "pr-review-queue", worker.Options{
		MaxConcurrentActivityExecutionSize: 10,
	})

	w.RegisterWorkflow(workflows.PRReviewWorkflow)
	w.RegisterWorkflow(workflows.FixFindingWorkflow)
	w.RegisterWorkflow(workflows.PollPRsWorkflow)
	w.RegisterWorkflow(workflows.FeedbackPollerWorkflow)

	// Register activities
	diffFetcher := activities.NewDiffFetcher(logger, ghClient)
	w.RegisterActivityWithOptions(
		diffFetcher.FetchDiff,
		activity.RegisterOptions{Name: activities.ActivityDiffFetcher},
	)

	w.RegisterActivityWithOptions(
		activities.NewSecurityAgent(eventBus, logger, llmClient, &cfg.Agents.Security, promptRegistry, metricsStore).Execute,
		activity.RegisterOptions{Name: activities.ActivitySecurity},
	)
	w.RegisterActivityWithOptions(
		activities.NewStyleAgent(eventBus, logger, llmClient, &cfg.Agents.Style, promptRegistry, metricsStore).Execute,
		activity.RegisterOptions{Name: activities.ActivityStyle},
	)
	w.RegisterActivityWithOptions(
		activities.NewLogicAgent(eventBus, logger, llmClient, &cfg.Agents.Logic, promptRegistry, metricsStore).Execute,
		activity.RegisterOptions{Name: activities.ActivityLogic},
	)
	w.RegisterActivityWithOptions(
		activities.NewDocsAgent(eventBus, logger, llmClient, &cfg.Agents.Documentation, promptRegistry, metricsStore).Execute,
		activity.RegisterOptions{Name: activities.ActivityDocs},
	)
	w.RegisterActivityWithOptions(
		(&activities.SynthesisAgent{EventBus: eventBus, Logger: logger}).Execute,
		activity.RegisterOptions{Name: activities.ActivitySynthesis},
	)
	w.RegisterActivityWithOptions(
		activities.NewTriageAgent(eventBus, logger, llmClient, &cfg.Agents.Triage, promptRegistry).Execute,
		activity.RegisterOptions{Name: activities.ActivityTriage},
	)

	githubActivity := activities.NewGitHubActivity(ghClient, logger)
	w.RegisterActivityWithOptions(
		githubActivity.GetPRHeadSHA,
		activity.RegisterOptions{Name: activities.ActivityGetPRHeadSHA},
	)
	w.RegisterActivityWithOptions(
		githubActivity.ReadFile,
		activity.RegisterOptions{Name: activities.ActivityReadFile},
	)

	fixGenerator := activities.NewFixGeneratorActivity(llmClient, &cfg.Agents.FixGenerator, logger)
	w.RegisterActivityWithOptions(
		fixGenerator.Execute,
		activity.RegisterOptions{Name: activities.ActivityGenerateFix},
	)

	coalesceActivity := activities.NewCoalesceActivity(ghClient, logger)
	w.RegisterActivityWithOptions(
		coalesceActivity.Execute,
		activity.RegisterOptions{Name: activities.ActivityCoalesce},
	)

	createPRActivity := activities.NewCreatePRActivity(ghClient, logger)
	w.RegisterActivityWithOptions(
		createPRActivity.Execute,
		activity.RegisterOptions{Name: activities.ActivityCreatePR},
	)

	listPRsActivity := activities.NewListPRsActivity(ghClient, logger, cfg.Poller.Filters)
	w.RegisterActivityWithOptions(
		listPRsActivity.ListOpenPRs,
		activity.RegisterOptions{Name: activities.ActivityListOpenPRs},
	)

	metricsActivity := activities.NewMetricsActivity(metricsStore, logger)
	w.RegisterActivityWithOptions(
		metricsActivity.HasReviewedAtSHA,
		activity.RegisterOptions{Name: activities.ActivityHasReviewedAtSHA},
	)
	w.RegisterActivityWithOptions(
		metricsActivity.RecordSkip,
		activity.RegisterOptions{Name: activities.ActivityRecordSkip},
	)

	feedbackPollerActivity := activities.NewFeedbackPollerActivity(ghClient, metricsStore, logger, reviewStore)
	w.RegisterActivityWithOptions(
		feedbackPollerActivity.CheckFeedback,
		activity.RegisterOptions{Name: activities.ActivityCheckFeedback},
	)

	postReviewActivity := activities.NewPostReviewActivity(ghClient, logger, reviewStore, metricsStore)
	w.RegisterActivityWithOptions(
		postReviewActivity.PostReview,
		activity.RegisterOptions{Name: activities.ActivityPostReview},
	)
	w.RegisterActivityWithOptions(
		postReviewActivity.HasPendingReview,
		activity.RegisterOptions{Name: activities.ActivityHasPendingReview},
	)

	// Start worker in background
	go func() {
		if err := w.Run(worker.InterruptCh()); err != nil {
			logger.Fatal("Worker failed", zap.Error(err))
		}
	}()

	// Start dashboard server
	go func() {
		dashboardServer := dashboard.NewServer(eventBus, logger)
		if err := dashboardServer.Start(cfg.Server.DashboardAddress); err != nil {
			logger.Fatal("Dashboard server failed", zap.Error(err))
		}
	}()

	// Upsert poller schedule if enabled
	if cfg.Poller.Enabled {
		if ghClient == nil {
			logger.Warn("Poller enabled but GITHUB_TOKEN not set — skipping schedule creation")
		} else {
			upsertPollerSchedule(context.Background(), temporalClient, cfg, logger)
		}
	}

	// Build HTTP mux
	mux := http.NewServeMux()

	// Health endpoints — unauthenticated
	mux.HandleFunc("/health", healthHandler(temporalClient, metricsStore))

	// Webhook — no auth (validated via HMAC)
	if cfg.Webhook.Enabled {
		webhookHandler := webhook.NewHandler(temporalClient, logger, cfg)
		mux.HandleFunc("/webhook/pr", webhookHandler.HandlePR)
	} else {
		mux.HandleFunc("/webhook/pr", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "Webhook is disabled", http.StatusNotFound)
		})
	}

	// Admin API — bearer token auth required
	adminAuth := middleware.BearerAuth(cfg.Admin.Token)

	reviewsHandler := reviews.NewHandler(reviewStore, ghClient, logger)
	mux.Handle("/api/reviews", adminAuth(reviewsHandler))
	mux.Handle("/api/reviews/stream", adminAuth(reviewsHandler))
	mux.Handle("/api/reviews/submit", adminAuth(reviewsHandler))
	mux.Handle("/api/reviews/skip", adminAuth(http.HandlerFunc(skipHandler(metricsStore, logger))))
	mux.Handle("/api/reviews/delete", adminAuth(http.HandlerFunc(deleteReviewHandler(metricsStore, logger))))
	mux.Handle("/api/reviews/force", adminAuth(http.HandlerFunc(forceReviewHandler(metricsStore, ghClient, temporalClient, logger))))
	mux.Handle("/api/feedback", adminAuth(http.HandlerFunc(feedbackHandler(metricsStore, logger))))
	mux.Handle("/api/metrics", adminAuth(http.HandlerFunc(metricsHandler(metricsStore, logger))))

	server := &http.Server{
		Addr:              cfg.Server.BindAddress,
		Handler:           mux,
		ReadTimeout:       time.Duration(cfg.Server.ReadTimeout) * time.Second,
		WriteTimeout:      time.Duration(cfg.Server.WriteTimeout) * time.Second,
		IdleTimeout:       time.Duration(cfg.Server.IdleTimeout) * time.Second,
		MaxHeaderBytes:    cfg.Server.MaxHeaderBytes,
		ReadHeaderTimeout: 10 * time.Second,
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		logger.Info("Shutting down gracefully...")
		w.Stop()
		server.Close()
	}()

	logger.Info("Service started",
		zap.String("dashboard", "http://"+cfg.Server.DashboardAddress),
		zap.String("api", "http://"+cfg.Server.BindAddress))

	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Fatal("Server failed", zap.Error(err))
	}

	logger.Info("Service stopped")
}

// healthHandler returns system health status without exposing sensitive config.
func healthHandler(temporalClient client.Client, metricsStore *metricsqlite.Store) http.HandlerFunc {
	type healthStatus struct {
		Status   string `json:"status"`
		Temporal string `json:"temporal"`
		SQLite   string `json:"sqlite"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		status := healthStatus{Status: "ok"}

		// Check Temporal by verifying the client is non-nil (connected at startup)
		if temporalClient == nil {
			status.Temporal = "error"
			status.Status = "degraded"
		} else {
			status.Temporal = "ok"
		}

		// Check SQLite
		if err := metricsStore.Ping(r.Context()); err != nil {
			status.SQLite = "error"
			status.Status = "degraded"
		} else {
			status.SQLite = "ok"
		}

		w.Header().Set("Content-Type", "application/json")
		if status.Status != "ok" {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		json.NewEncoder(w).Encode(status)
	}
}

func feedbackHandler(repo metrics.Repository, logger *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			FindingID string `json:"finding_id"`
			Verdict   string `json:"verdict"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if body.FindingID == "" || body.Verdict == "" {
			http.Error(w, "finding_id and verdict required", http.StatusBadRequest)
			return
		}
		if err := repo.SaveFeedback(r.Context(), metrics.FeedbackEvent{
			FindingID: body.FindingID,
			Verdict:   body.Verdict,
			Source:    "manual",
		}); err != nil {
			logger.Error("Failed to save feedback", zap.Error(err))
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func skipHandler(repo metrics.Repository, logger *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			RepoOwner string `json:"repo_owner"`
			RepoName  string `json:"repo_name"`
			PRNumber  int    `json:"pr_number"`
			HeadSHA   string `json:"head_sha"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if body.RepoOwner == "" || body.RepoName == "" || body.PRNumber == 0 || body.HeadSHA == "" {
			http.Error(w, "repo_owner, repo_name, pr_number, and head_sha are required", http.StatusBadRequest)
			return
		}
		if err := repo.RecordSkip(r.Context(), body.RepoOwner, body.RepoName, body.PRNumber, body.HeadSHA); err != nil {
			logger.Error("Failed to record PR skip", zap.Error(err))
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func deleteReviewHandler(repo metrics.Repository, logger *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			RepoOwner string `json:"repo_owner"`
			RepoName  string `json:"repo_name"`
			PRNumber  int    `json:"pr_number"`
			HeadSHA   string `json:"head_sha"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if body.RepoOwner == "" || body.RepoName == "" || body.PRNumber == 0 {
			http.Error(w, "repo_owner, repo_name, and pr_number are required", http.StatusBadRequest)
			return
		}
		if err := repo.DeleteReviewRun(r.Context(), body.RepoOwner, body.RepoName, body.PRNumber, body.HeadSHA); err != nil {
			logger.Error("Failed to delete review records", zap.Error(err))
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func forceReviewHandler(repo metrics.Repository, ghClient *github.Client, temporalClient client.Client, logger *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			RepoOwner string `json:"repo_owner"`
			RepoName  string `json:"repo_name"`
			PRNumber  int    `json:"pr_number"`
			HeadSHA   string `json:"head_sha"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if body.RepoOwner == "" || body.RepoName == "" || body.PRNumber == 0 {
			http.Error(w, "repo_owner, repo_name, and pr_number are required", http.StatusBadRequest)
			return
		}

		input := types.PRReviewInput{
			PRNumber:  body.PRNumber,
			RepoOwner: body.RepoOwner,
			RepoName:  body.RepoName,
			HeadSHA:   body.HeadSHA,
		}
		if ghClient != nil {
			pr, _, err := ghClient.PullRequests.Get(r.Context(), body.RepoOwner, body.RepoName, body.PRNumber)
			if err != nil {
				logger.Error("Failed to fetch PR from GitHub", zap.Error(err))
				http.Error(w, "failed to fetch PR from GitHub", http.StatusBadGateway)
				return
			}
			if body.HeadSHA == "" {
				input.HeadSHA = pr.GetHead().GetSHA()
			}
			input.Title = pr.GetTitle()
			input.DiffURL = pr.GetDiffURL()
			input.HeadBranch = pr.GetHead().GetRef()
			input.BaseBranch = pr.GetBase().GetRef()
			input.PRAuthor = pr.GetUser().GetLogin()
		}
		if input.HeadSHA == "" {
			http.Error(w, "head_sha is required: GITHUB_TOKEN not configured", http.StatusBadRequest)
			return
		}

		if err := repo.DeleteReviewRun(r.Context(), body.RepoOwner, body.RepoName, body.PRNumber, body.HeadSHA); err != nil {
			logger.Error("Failed to delete review run record", zap.Error(err))
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		shortSHA := input.HeadSHA
		if len(shortSHA) > 8 {
			shortSHA = shortSHA[:8]
		}
		workflowID := fmt.Sprintf("pr-review/%s/%s/%d/%s", body.RepoOwner, body.RepoName, body.PRNumber, shortSHA)

		run, err := temporalClient.ExecuteWorkflow(r.Context(), client.StartWorkflowOptions{
			ID:                    workflowID,
			TaskQueue:             "pr-review-queue",
			WorkflowIDReusePolicy: enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
		}, workflows.PRReviewWorkflow, input)
		if err != nil {
			logger.Error("Failed to start force-review workflow", zap.Error(err))
			http.Error(w, "failed to start workflow", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"workflow_id": workflowID,
			"run_id":      run.GetRunID(),
			"head_sha":    input.HeadSHA,
		})
	}
}

func metricsHandler(repo metrics.Repository, logger *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		since := time.Now().AddDate(0, -1, 0)
		if s := r.URL.Query().Get("since"); s != "" {
			if t, err := time.Parse(time.RFC3339, s); err == nil {
				since = t
			}
		}
		results, err := repo.ListAgentMetrics(r.Context(), since)
		if err != nil {
			logger.Error("Failed to list agent metrics", zap.Error(err))
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(results)
	}
}

func upsertPollerSchedule(ctx context.Context, temporalClient client.Client, cfg *config.Config, logger *zap.Logger) {
	const scheduleID = "poll-github-prs"

	pollInput := types.PollPRsInput{
		Repos:        cfg.Poller.Repos,
		AutoFixUsers: cfg.AutoFixUsers,
	}

	stepMinutes := cfg.Poller.Interval / 60
	if stepMinutes < 1 {
		stepMinutes = 30
	}

	spec := client.ScheduleSpec{
		Calendars: []client.ScheduleCalendarSpec{
			{
				Minute:    []client.ScheduleRange{{Start: 0, End: 59, Step: stepMinutes}},
				Hour:      []client.ScheduleRange{{Start: 8, End: 17}},
				DayOfWeek: []client.ScheduleRange{{Start: 1, End: 5}},
			},
		},
		TimeZoneName: "America/New_York",
	}
	action := &client.ScheduleWorkflowAction{
		Workflow:  workflows.PollPRsWorkflow,
		TaskQueue: "pr-review-queue",
		Args:      []interface{}{pollInput},
	}

	scheduleClient := temporalClient.ScheduleClient()
	handle := scheduleClient.GetHandle(ctx, scheduleID)
	err := handle.Update(ctx, client.ScheduleUpdateOptions{
		DoUpdate: func(input client.ScheduleUpdateInput) (*client.ScheduleUpdate, error) {
			input.Description.Schedule.Spec = &spec
			input.Description.Schedule.Action = action
			input.Description.Schedule.Policy = &client.SchedulePolicies{
				Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
			}
			return &client.ScheduleUpdate{Schedule: &input.Description.Schedule}, nil
		},
	})
	if err == nil {
		logger.Info("Updated existing poller schedule", zap.String("schedule_id", scheduleID))
		return
	}

	var notFound *serviceerror.NotFound
	if !errors.As(err, &notFound) {
		logger.Error("Failed to update poller schedule", zap.Error(err))
		return
	}

	_, err = scheduleClient.Create(ctx, client.ScheduleOptions{
		ID:      scheduleID,
		Spec:    spec,
		Action:  action,
		Overlap: enumspb.SCHEDULE_OVERLAP_POLICY_SKIP,
	})
	if err != nil {
		logger.Error("Failed to create poller schedule", zap.Error(err))
		return
	}
	logger.Info("Created poller schedule", zap.String("schedule_id", scheduleID))
}
