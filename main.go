package main

import (
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/rikdc/temporal-code-reviewer/activities"
	"github.com/rikdc/temporal-code-reviewer/config"
	"github.com/rikdc/temporal-code-reviewer/dashboard"
	"github.com/rikdc/temporal-code-reviewer/events"
	"github.com/rikdc/temporal-code-reviewer/llm"
	"github.com/rikdc/temporal-code-reviewer/webhook"
	"github.com/rikdc/temporal-code-reviewer/workflows"
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

	// Initialize prompt loader
	promptLoader := llm.NewPromptLoader("prompts")

	// Initialize event bus
	eventBus := events.NewEventBus()

	// Get Temporal address from environment
	temporalAddress := os.Getenv("TEMPORAL_ADDRESS")
	if temporalAddress == "" {
		temporalAddress = "localhost:7233"
	}

	// Connect to Temporal
	logger.Info("Connecting to Temporal", zap.String("address", temporalAddress))
	temporalClient, err := client.Dial(client.Options{
		HostPort: temporalAddress,
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

	// Register workflow
	w.RegisterWorkflow(workflows.PRReviewWorkflow)

	// Create diff fetcher activity
	diffFetcher := activities.NewDiffFetcher(logger)
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

	// Start webhook server
	logger.Info("Starting webhook server on :8082")
	webhookHandler := webhook.NewHandler(temporalClient, logger)

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
