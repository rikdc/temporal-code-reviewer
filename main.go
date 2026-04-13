package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/google/go-github/v68/github"
	"github.com/rikdc/temporal-code-reviewer/activities"
	"github.com/rikdc/temporal-code-reviewer/config"
	"github.com/rikdc/temporal-code-reviewer/dashboard"
	"github.com/rikdc/temporal-code-reviewer/events"
	"github.com/rikdc/temporal-code-reviewer/llm"
	"github.com/rikdc/temporal-code-reviewer/types"
	"github.com/rikdc/temporal-code-reviewer/webhook"
	"github.com/rikdc/temporal-code-reviewer/workflows"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	enumspb "go.temporal.io/api/enums/v1"
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

	// Initialize prompt loader
	promptLoader := llm.NewPromptLoader("prompts")

	// Initialize event bus
	eventBus := events.NewEventBus()

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
		activities.NewSecurityAgent(eventBus, logger, llmClient, &cfg.Agents.Security, promptLoader).Execute,
		activity.RegisterOptions{Name: activities.ActivitySecurity},
	)
	w.RegisterActivityWithOptions(
		activities.NewStyleAgent(eventBus, logger, llmClient, &cfg.Agents.Style, promptLoader).Execute,
		activity.RegisterOptions{Name: activities.ActivityStyle},
	)
	w.RegisterActivityWithOptions(
		activities.NewLogicAgent(eventBus, logger, llmClient, &cfg.Agents.Logic, promptLoader).Execute,
		activity.RegisterOptions{Name: activities.ActivityLogic},
	)
	w.RegisterActivityWithOptions(
		activities.NewDocsAgent(eventBus, logger, llmClient, &cfg.Agents.Documentation, promptLoader).Execute,
		activity.RegisterOptions{Name: activities.ActivityDocs},
	)
	w.RegisterActivityWithOptions(
		(&activities.SynthesisAgent{EventBus: eventBus, Logger: logger}).Execute,
		activity.RegisterOptions{Name: activities.ActivitySynthesis},
	)

	// Register triage agent
	triageAgent := activities.NewTriageAgent(eventBus, logger, llmClient, &cfg.Agents.Triage, promptLoader)
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

	// Register post review activity
	postReviewActivity := activities.NewPostReviewActivity(ghClient, logger)
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
	webhookHandler := webhook.NewHandler(temporalClient, logger, cfg.AutoFixUsers, cfg.Temporal.DashboardBaseURL)

	mux := http.NewServeMux()
	mux.HandleFunc("/webhook/pr", webhookHandler.HandlePR)
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

// upsertPollerSchedule creates or updates the Temporal Schedule that drives
// GitHub PR polling. It is idempotent — safe to call on every startup.
//
// The schedule runs every 15 minutes during business hours only:
// Monday–Friday, 08:00–17:45 America/New_York (last fire at 17:45, done by 18:00).
func upsertPollerSchedule(ctx context.Context, temporalClient client.Client, cfg *config.Config, logger *zap.Logger) {
	const scheduleID = "poll-github-prs"

	pollInput := types.PollPRsInput{
		Repos:        cfg.Poller.Repos,
		AutoFixUsers: cfg.AutoFixUsers,
	}

	// Every 15 min, Mon–Fri, 08:00–17:59 ET.
	spec := client.ScheduleSpec{
		Calendars: []client.ScheduleCalendarSpec{
			{
				Minute:    []client.ScheduleRange{{Start: 0, End: 59, Step: 15}},
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
