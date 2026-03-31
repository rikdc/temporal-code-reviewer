package workflows

import (
	"time"

	"github.com/rikdc/temporal-code-reviewer/activities"
	"github.com/rikdc/temporal-code-reviewer/types"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

// FixFindingWorkflow is a child workflow that fixes a single auto-fixable finding.
// It reads the current file from GitHub, then generates a fix via LLM.
func FixFindingWorkflow(ctx workflow.Context, input types.FixFindingInput) (types.FixResult, error) {
	logger := workflow.GetLogger(ctx)
	finding := input.Decision.Finding

	logger.Info("Fix finding workflow started",
		"finding", finding.Title,
		"file", finding.File)

	// Activity A: Read the file from GitHub
	readFileOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 15 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 2,
		},
	}
	readCtx := workflow.WithActivityOptions(ctx, readFileOpts)

	// Prefer the commit SHA — it's immutable and works for deleted branches and forks.
	// Fall back to the branch name if the SHA wasn't captured by the webhook.
	ref := input.HeadSHA
	if ref == "" {
		ref = input.HeadBranch
	}

	var fileContent string
	readInput := types.ReadFileInput{
		RepoOwner: input.RepoOwner,
		RepoName:  input.RepoName,
		FilePath:  finding.File,
		Ref:       ref,
	}
	if err := workflow.ExecuteActivity(readCtx, activities.ActivityReadFile, readInput).Get(ctx, &fileContent); err != nil {
		logger.Error("Failed to read file", "error", err, "file", finding.File)
		return types.FixResult{
			FindingID:     finding.Title,
			Success:       false,
			FailureReason: err.Error(),
		}, nil
	}

	// Activity B: Generate the fix
	fixOpts := workflow.ActivityOptions{
		StartToCloseTimeout: 20 * time.Second,
		RetryPolicy: &temporal.RetryPolicy{
			MaximumAttempts: 2,
		},
	}
	fixCtx := workflow.WithActivityOptions(ctx, fixOpts)

	var fixResult types.FixResult
	fixInput := types.GenerateFixInput{
		Decision:    input.Decision,
		FileContent: fileContent,
	}
	if err := workflow.ExecuteActivity(fixCtx, activities.ActivityGenerateFix, fixInput).Get(ctx, &fixResult); err != nil {
		logger.Error("Failed to generate fix", "error", err, "finding", finding.Title)
		return types.FixResult{
			FindingID:     finding.Title,
			Success:       false,
			FailureReason: err.Error(),
		}, nil
	}

	logger.Info("Fix finding workflow completed",
		"finding", finding.Title,
		"success", fixResult.Success)

	return fixResult, nil
}
