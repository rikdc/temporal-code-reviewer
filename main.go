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
	// Initialize logger
	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatalf("Failed to create logger: %v", err)
	}
	defer logger.Sync()

	logger.Info("Starting Temporal Code Review Service")

	// Load configuration
	logger.Info("Loading configuration from config.yaml")
	cfg, err := config.Load("config.yaml")
	if err != nil {
		logger.Fatal("Failed to load config", zap.Error(err))
	}
	logger.Info("Configuration loaded successfully",
		zap.String("openrouter_url", cfg.OpenRouter.BaseURL),
		zap.Bool("api_key_set", cfg.OpenRouter.APIKey != ""))

	// Initialize LLM client
	logger.Info("Initializing OpenRouter LLM client")
	llmClient := llm.NewClient(&cfg.OpenRouter, logger)

	// Initialize prompt loader (disk fallback)
	promptLoader := llm.NewPromptLoader("prompts")

	// Initialize metrics store
	homeDir, err := os.UserHomeDir()
	if err != nil {
		logger.Fatal("Failed to get home directory", zap.Error(err))
	}
	metricsDBPath := filepath.Join(homeDir, ".config", "prism", "metrics.db")
	metricsStore, err := metricsqlite.Open(metricsDBPath)
	if err != nil {
		logger.Fatal("Failed to open metrics database", zap.String("path", metricsDBPath), zap.Error(err))
	}
	defer metricsStore.Close()
	logger.Info("Metrics database opened", zap.String("path", metricsDBPath))

	// Seed prompt versions from disk if no DB versions exist yet.
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

	// Build prompt registry (A/B selection backed by DB).
	promptRegistry := metrics.NewPromptRegistry(metricsStore, promptLoader)

	// Initialize event bus
	eventBus := events.NewEventBus()

	// Initialize review store (in-memory, feeds SSE dashboard)
	reviewStore := reviews.NewStore()

	// Get Temporal address from environment
	temporalAddress := os.Getenv("TEMPORAL_ADDRESS")
	if temporalAddress == "" {
		temporalAddress = "localhost:7233"
	}

	// Get GitHub token and create SDK client
	githubToken := os.Getenv("GITHUB_TOKEN")
	var ghClient *github.Client
	if githubToken != "" {
		ghClient = github.NewClient(nil).WithAuthToken(githubToken)
	} else {
		logger.Warn("GITHUB_TOKEN not set — triage auto-fix features will not work")
	}

	// Connect to Temporal
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
	logger.Info("Creating Temporal worker")
	w := worker.New(temporalClient, "pr-review-queue", worker.Options{
		MaxConcurrentActivityExecutionSize: 10,
	})

	// Register workflows
	w.RegisterWorkflow(workflows.PRReviewWorkflow)
	w.RegisterWorkflow(workflows.FixFindingWorkflow)
	w.RegisterWorkflow(workflows.PollPRsWorkflow)

	// Create diff fetcher activity
	diffFetcher := activities.NewDiffFetcher(logger, ghClient)
	w.RegisterActivityWithOptions(
		diffFetcher.FetchDiff,
		activity.RegisterOptions{Name: activities.ActivityDiffFetcher},
	)

	// Register review agents with LLM integration
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

	// Register triage agent
	triageAgent := activities.NewTriageAgent(eventBus, logger, llmClient, &cfg.Agents.Triage, promptRegistry)
	w.RegisterActivityWithOptions(
		triageAgent.Execute,
		activity.RegisterOptions{Name: activities.ActivityTriage},
	)

	// Register GitHub activity
	githubActivity := activities.NewGitHubActivity(ghClient, logger)
	w.RegisterActivityWithOptions(
		githubActivity.GetPRHeadSHA,
		activity.RegisterOptions{Name: activities.ActivityGetPRHeadSHA},
	)
	w.RegisterActivityWithOptions(
		githubActivity.ReadFile,
		activity.RegisterOptions{Name: activities.ActivityReadFile},
	)

	// Register fix generator
	fixGenerator := activities.NewFixGeneratorActivity(llmClient, &cfg.Agents.FixGenerator, logger)
	w.RegisterActivityWithOptions(
		fixGenerator.Execute,
		activity.RegisterOptions{Name: activities.ActivityGenerateFix},
	)

	// Register coalesce activity
	coalesceActivity := activities.NewCoalesceActivity(ghClient, logger)
	w.RegisterActivityWithOptions(
		coalesceActivity.Execute,
		activity.RegisterOptions{Name: activities.ActivityCoalesce},
	)

	// Register PR creation activity
	createPRActivity := activities.NewCreatePRActivity(ghClient, logger)
	w.RegisterActivityWithOptions(
		createPRActivity.Execute,
		activity.RegisterOptions{Name: activities.ActivityCreatePR},
	)

	// Register list PRs activity (used by the polling workflow)
	listPRsActivity := activities.NewListPRsActivity(ghClient, logger, cfg.Poller.Filters)
	w.RegisterActivityWithOptions(
		listPRsActivity.ListOpenPRs,
		activity.RegisterOptions{Name: activities.ActivityListOpenPRs},
	)

	// Register metrics activities
	metricsActivity := activities.NewMetricsActivity(metricsStore, logger)
	w.RegisterActivityWithOptions(
		metricsActivity.HasReviewedAtSHA,
		activity.RegisterOptions{Name: activities.ActivityHasReviewedAtSHA},
	)
	w.RegisterActivityWithOptions(
		metricsActivity.RecordSkip,
		activity.RegisterOptions{Name: activities.ActivityRecordSkip},
	)

	// Register feedback poller activity and workflow
	feedbackPollerActivity := activities.NewFeedbackPollerActivity(ghClient, metricsStore, logger, reviewStore)
	w.RegisterActivityWithOptions(
		feedbackPollerActivity.CheckFeedback,
		activity.RegisterOptions{Name: activities.ActivityCheckFeedback},
	)
	w.RegisterWorkflow(workflows.FeedbackPollerWorkflow)

	// Register post review activity
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
	logger.Info("Starting Temporal worker")
	go func() {
		if err := w.Run(worker.InterruptCh()); err != nil {
			logger.Fatal("Worker failed", zap.Error(err))
		}
	}()

	// Start dashboard server in background
	logger.Info("Starting dashboard server on :8081")
	dashboardServer := dashboard.NewServer(eventBus, logger)
	go func() {
		if err := dashboardServer.Start(":8081"); err != nil {
			logger.Fatal("Dashboard server failed", zap.Error(err))
		}
	}()

	// Upsert Temporal Schedule for GitHub polling (if enabled)
	if cfg.Poller.Enabled {
		if ghClient == nil {
			logger.Warn("Poller enabled but GITHUB_TOKEN not set — skipping schedule creation")
		} else {
			upsertPollerSchedule(context.Background(), temporalClient, cfg, logger)
		}
	}

	// Start webhook server
	logger.Info("Starting webhook server on :8082")
	webhookHandler := webhook.NewHandler(temporalClient, logger, cfg.AutoFixUsers)
	reviewsHandler := reviews.NewHandler(reviewStore, ghClient, logger)

	mux := http.NewServeMux()
	mux.HandleFunc("/webhook/pr", webhookHandler.HandlePR)
	mux.HandleFunc("/api/reviews", reviewsHandler.HandleList)
	mux.HandleFunc("/api/reviews/stream", reviewsHandler.HandleStream)
	mux.HandleFunc("/api/reviews/submit", reviewsHandler.HandleSubmit)
	mux.HandleFunc("/api/reviews/skip", skipHandler(metricsStore, logger))
	mux.HandleFunc("/api/reviews/delete", deleteReviewHandler(metricsStore, logger))
	mux.HandleFunc("/api/reviews/force", forceReviewHandler(metricsStore, ghClient, temporalClient, logger))
	mux.HandleFunc("/api/feedback", feedbackHandler(metricsStore, logger))
	mux.HandleFunc("/api/metrics", metricsHandler(metricsStore, logger))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	server := &http.Server{
		Addr:    ":8082",
		Handler: mux,
	}

	// Handle graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-sigChan
		logger.Info("Shutting down gracefully...")
		w.Stop()
		server.Close()
	}()

	logger.Info("Service started",
		zap.String("dashboard", "http://localhost:8081"),
		zap.String("webhook", "http://localhost:8082"))

	// Start webhook server (blocking)
	if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Fatal("Webhook server failed", zap.Error(err))
	}

	logger.Info("Service stopped")
}

// feedbackHandler handles POST /api/feedback to record a manual verdict on a finding.
// Body: {"finding_id":"<uuid>","verdict":"fp|tp|ignored"}
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
		if body.FindingID == "" || (body.Verdict != "fp" && body.Verdict != "tp" && body.Verdict != "ignored") {
			http.Error(w, "finding_id and verdict (fp|tp|ignored) required", http.StatusBadRequest)
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

// skipHandler handles POST /api/reviews/skip to mark a PR+SHA as explicitly
// skipped. The poller's HasReviewedAtSHA check will return true for any
// PR+SHA recorded here, preventing re-review even when no review was posted
// (e.g. after discarding a draft review from the GitHub UI).
//
// Body: {"repo_owner":"...","repo_name":"...","pr_number":123,"head_sha":"abc123"}
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
		logger.Info("PR skip recorded via API",
			zap.String("repo", body.RepoOwner+"/"+body.RepoName),
			zap.Int("pr_number", body.PRNumber),
			zap.String("head_sha", body.HeadSHA))
		w.WriteHeader(http.StatusNoContent)
	}
}

// deleteReviewHandler handles DELETE /api/reviews.
// It clears the dedup records (review_runs + pr_skips) for a PR so the next
// poller cycle will pick it up for re-review. Unlike /api/reviews/force it
// does not immediately start a workflow.
//
// Body:
//
//	{
//	  "repo_owner": "acme",
//	  "repo_name":  "backend",
//	  "pr_number":  42,
//	  "head_sha":   "abc12345"  // optional; omit to clear all SHAs for the PR
//	}
//
// Response 204 No Content.
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
		logger.Info("Review records cleared via API",
			zap.String("repo", body.RepoOwner+"/"+body.RepoName),
			zap.Int("pr_number", body.PRNumber),
			zap.String("head_sha", body.HeadSHA))
		w.WriteHeader(http.StatusNoContent)
	}
}

// forceReviewHandler handles POST /api/reviews/force.
// It clears the dedup records for a PR and immediately starts a new
// PRReviewWorkflow, bypassing both the metrics-DB gate and the GitHub
// pending-review gate.
//
// Body:
//
//	{
//	  "repo_owner": "acme",
//	  "repo_name":  "backend",
//	  "pr_number":  42,
//	  "head_sha":   "abc12345"  // optional; resolved from GitHub if omitted
//	}
//
// Response 200 JSON: {"workflow_id":"...","run_id":"...","head_sha":"..."}
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

		// Fetch full PR metadata from GitHub. When head_sha was not supplied we
		// need the current HEAD; when it was supplied we still need title, diff
		// URL, etc. for PRReviewInput. One API call covers both cases.
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

		// Clear dedup records. Pass body.HeadSHA (possibly empty) so that when
		// the caller omitted it we delete all records for the PR number, and when
		// they supplied it we delete only that SHA's records.
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

		// ALLOW_DUPLICATE so the workflow starts unconditionally even if a prior
		// execution for the same ID already completed successfully.
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

		logger.Info("Force-review workflow started",
			zap.String("workflow_id", workflowID),
			zap.String("run_id", run.GetRunID()),
			zap.String("repo", body.RepoOwner+"/"+body.RepoName),
			zap.Int("pr_number", body.PRNumber),
			zap.String("head_sha", input.HeadSHA))

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"workflow_id": workflowID,
			"run_id":      run.GetRunID(),
			"head_sha":    input.HeadSHA,
		})
	}
}

// metricsHandler handles GET /api/metrics?since=<RFC3339> and returns agent metrics as JSON.
func metricsHandler(repo metrics.Repository, logger *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		since := time.Now().AddDate(0, -1, 0) // default: last 30 days
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

// upsertPollerSchedule creates or updates the Temporal Schedule that drives
// GitHub PR polling. It is idempotent — safe to call on every startup.
//
// The schedule cadence is controlled by cfg.Poller.Interval (interval_seconds in
// config.yaml). It fires during business hours only: Monday–Friday, 08:00–17:59
// America/New_York.
func upsertPollerSchedule(ctx context.Context, temporalClient client.Client, cfg *config.Config, logger *zap.Logger) {
	const scheduleID = "poll-github-prs"

	pollInput := types.PollPRsInput{
		Repos:        cfg.Poller.Repos,
		AutoFixUsers: cfg.AutoFixUsers,
	}

	stepMinutes := cfg.Poller.Interval / 60
	if stepMinutes < 1 {
		stepMinutes = 15 // default: 15 minutes
	}

	// Mon–Fri, 08:00–17:59 ET, firing every stepMinutes minutes.
	spec := client.ScheduleSpec{
		Calendars: []client.ScheduleCalendarSpec{
			{
				Minute:    []client.ScheduleRange{{Start: 0, End: 59, Step: stepMinutes}},
				Hour:      []client.ScheduleRange{{Start: 8, End: 17}},
				DayOfWeek: []client.ScheduleRange{{Start: 1, End: 5}}, // Mon=1, Fri=5
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

	// Try to update an existing schedule first.
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
		logger.Info("Updated existing poller schedule",
			zap.String("schedule_id", scheduleID),
			zap.Strings("repos", cfg.Poller.Repos))
		return
	}

	// Only fall through to Create when the schedule genuinely doesn't exist yet.
	// Any other error (network failure, permission denied, etc.) is non-recoverable
	// here and should not trigger a Create attempt, which would fail with AlreadyExists
	// and obscure the real cause.
	var notFound *serviceerror.NotFound
	if !errors.As(err, &notFound) {
		logger.Error("Failed to update poller schedule", zap.Error(err))
		return
	}

	// Schedule doesn't exist yet — create it.
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

	logger.Info("Created poller schedule",
		zap.String("schedule_id", scheduleID),
		zap.Strings("repos", cfg.Poller.Repos))
}
