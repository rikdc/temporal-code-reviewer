package activities

import (
	"bytes"
	"context"
	"fmt"

	"github.com/google/go-github/v68/github"
	"github.com/rikdc/temporal-code-reviewer/types"
	"go.uber.org/zap"
)

// CreatePRActivity creates a GitHub pull request for auto-fixes.
type CreatePRActivity struct {
	client *github.Client
	logger *zap.Logger
}

// NewCreatePRActivity creates a new CreatePRActivity.
func NewCreatePRActivity(client *github.Client, logger *zap.Logger) *CreatePRActivity {
	return &CreatePRActivity{
		client: client,
		logger: logger,
	}
}

// Execute creates a pull request with the coalesced fixes.
func (a *CreatePRActivity) Execute(ctx context.Context, input types.CreatePRInput) (types.CreatePRResult, error) {
	if a.client == nil {
		return types.CreatePRResult{}, fmt.Errorf("GitHub client not configured: GITHUB_TOKEN is required for PR creation")
	}

	a.logger.Info("Creating fix PR",
		zap.String("branch", input.Changeset.BranchName),
		zap.String("target", input.OriginalBranch),
		zap.Int("original_pr", input.OriginalPRNum))

	// Idempotency: check for existing open PR from this branch
	existing, found, err := a.findExistingPR(ctx, input.RepoOwner, input.RepoName, input.Changeset.BranchName, input.OriginalBranch)
	if err != nil {
		a.logger.Warn("Failed to check for existing PR", zap.Error(err))
	} else if found {
		a.logger.Info("Found existing PR, returning it",
			zap.Int("pr_number", existing.PRNumber))
		return existing, nil
	}

	// Ensure labels exist
	a.ensureLabel(ctx, input.RepoOwner, input.RepoName, "ai-generated", "0075ca")
	a.ensureLabel(ctx, input.RepoOwner, input.RepoName, "code-review-fix", "1d76db")

	title := fmt.Sprintf("fix(ai-review): automated fixes for PR #%d", input.OriginalPRNum)
	body := buildPRBody(input)

	pr, _, err := a.client.PullRequests.Create(ctx, input.RepoOwner, input.RepoName, &github.NewPullRequest{
		Title: github.Ptr(title),
		Body:  github.Ptr(body),
		Head:  github.Ptr(input.Changeset.BranchName),
		Base:  github.Ptr(input.OriginalBranch),
	})
	if err != nil {
		return types.CreatePRResult{}, fmt.Errorf("create PR: %w", err)
	}

	// Add labels
	a.addLabels(ctx, input.RepoOwner, input.RepoName, pr.GetNumber(), []string{"ai-generated", "code-review-fix"})

	a.logger.Info("Fix PR created",
		zap.Int("pr_number", pr.GetNumber()),
		zap.String("url", pr.GetHTMLURL()))

	return types.CreatePRResult{
		PRNumber: pr.GetNumber(),
		PRURL:    pr.GetHTMLURL(),
	}, nil
}

func (a *CreatePRActivity) findExistingPR(ctx context.Context, owner, repo, head, base string) (types.CreatePRResult, bool, error) {
	prs, _, err := a.client.PullRequests.List(ctx, owner, repo, &github.PullRequestListOptions{
		State: "open",
		Head:  owner + ":" + head,
		Base:  base,
	})
	if err != nil {
		return types.CreatePRResult{}, false, err
	}

	if len(prs) > 0 {
		return types.CreatePRResult{
			PRNumber: prs[0].GetNumber(),
			PRURL:    prs[0].GetHTMLURL(),
		}, true, nil
	}

	return types.CreatePRResult{}, false, nil
}

func (a *CreatePRActivity) ensureLabel(ctx context.Context, owner, repo, name, color string) {
	_, _, err := a.client.Issues.CreateLabel(ctx, owner, repo, &github.Label{
		Name:  github.Ptr(name),
		Color: github.Ptr(color),
	})
	// Ignore errors — label may already exist (422)
	_ = err
}

func (a *CreatePRActivity) addLabels(ctx context.Context, owner, repo string, prNumber int, labels []string) {
	// Best-effort — label application is non-critical; don't fail the PR creation
	_, _, err := a.client.Issues.AddLabelsToIssue(ctx, owner, repo, prNumber, labels)
	_ = err
}

// buildPRBody generates the markdown body for the fix PR.
func buildPRBody(input types.CreatePRInput) string {
	var b bytes.Buffer

	fmt.Fprintf(&b, "## AI-implemented fixes\n\n")
	fmt.Fprintf(&b, "These changes were applied automatically based on the code review of PR #%d.\n\n", input.OriginalPRNum)

	if len(input.Changeset.Applied) > 0 {
		fmt.Fprintf(&b, "### Applied (%d)\n", len(input.Changeset.Applied))
		for _, fix := range input.Changeset.Applied {
			files := ""
			if len(fix.FilesChanged) > 0 {
				files = fmt.Sprintf(" (`%s`)", fix.FilesChanged[0])
			}
			fmt.Fprintf(&b, "- **%s**%s\n", fix.FindingID, files)
		}
		b.WriteString("\n")
	}

	if len(input.HumanRequired) > 0 {
		fmt.Fprintf(&b, "### Deferred — human review required (%d)\n", len(input.HumanRequired))
		for _, d := range input.HumanRequired {
			fmt.Fprintf(&b, "- **%s** (%s) — %s\n", d.Finding.Title, d.Finding.Severity, d.Reason)
		}
		b.WriteString("\n")
	}

	if len(input.Changeset.Conflicts) > 0 {
		fmt.Fprintf(&b, "### Conflicts — skipped (%d)\n", len(input.Changeset.Conflicts))
		for _, fix := range input.Changeset.Conflicts {
			fmt.Fprintf(&b, "- **%s** — %s\n", fix.FindingID, fix.FailureReason)
		}
		b.WriteString("\n")
	}

	b.WriteString("---\n_Review each change before merging this PR into your feature branch._\n")

	return b.String()
}
