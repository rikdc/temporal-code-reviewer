package activities

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/go-github/v68/github"
	"github.com/rikdc/temporal-code-reviewer/types"
	"go.uber.org/zap"
)

// CoalesceActivity merges fix results into a single branch.
type CoalesceActivity struct {
	client *github.Client
	logger *zap.Logger
}

// NewCoalesceActivity creates a new CoalesceActivity.
func NewCoalesceActivity(client *github.Client, logger *zap.Logger) *CoalesceActivity {
	return &CoalesceActivity{
		client: client,
		logger: logger,
	}
}

// Execute merges all successful fix diffs into a new branch.
func (a *CoalesceActivity) Execute(ctx context.Context, input types.CoalesceInput) (types.CoalescedChangeset, error) {
	if a.client == nil {
		return types.CoalescedChangeset{}, fmt.Errorf("GitHub client not configured: GITHUB_TOKEN is required for branch operations")
	}

	// Filter to successful fixes only
	var successful []types.FixResult
	for _, r := range input.FixResults {
		if r.Success {
			successful = append(successful, r)
		}
	}

	if len(successful) == 0 {
		a.logger.Info("No successful fixes to coalesce")
		return types.CoalescedChangeset{}, nil
	}

	// Derive branch name deterministically so that activity retries find the same
	// branch via the idempotency check below instead of creating orphans.
	shortSHA := input.HeadSHA
	if len(shortSHA) > 8 {
		shortSHA = shortSHA[:8]
	}
	branchName := fmt.Sprintf("ai-fixes/pr-%d-%s", input.PRNumber, shortSHA)

	a.logger.Info("Coalescing fixes",
		zap.Int("successful_count", len(successful)),
		zap.String("branch", branchName),
		zap.String("base", input.HeadBranch))

	// Use the commit SHA from the workflow input directly — this is more reliable than
	// resolving the branch name, which may not exist (deleted branch, fork PR, etc.).
	headSHA := input.HeadSHA

	// Check if branch already exists (idempotency for workflow replay)
	existingSHA, err := a.getBranchSHA(ctx, input.RepoOwner, input.RepoName, branchName)
	if err == nil && existingSHA != "" {
		a.logger.Info("Branch already exists, checking for existing commit",
			zap.String("branch", branchName))
		return types.CoalescedChangeset{
			Applied:    successful,
			BranchName: branchName,
		}, nil
	}

	// Create branch from head
	if err := a.createBranch(ctx, input.RepoOwner, input.RepoName, branchName, headSHA); err != nil {
		return types.CoalescedChangeset{}, fmt.Errorf("create branch: %w", err)
	}

	var applied []types.FixResult
	var conflicts []types.FixResult

	// Track which files already have an applied fix.
	// If a second fix touches the same file, it's a conflict.
	appliedFiles := make(map[string]bool)

	for _, fix := range successful {
		conflicting := false
		for _, f := range fix.FilesChanged {
			if appliedFiles[f] {
				conflicting = true
				break
			}
		}

		if conflicting {
			conflicts = append(conflicts, types.FixResult{
				FindingID:     fix.FindingID,
				Success:       false,
				Diff:          fix.Diff,
				FilesChanged:  fix.FilesChanged,
				CommitMsg:     fix.CommitMsg,
				FailureReason: "conflicting change to same file as another fix",
			})
			continue
		}

		for _, f := range fix.FilesChanged {
			appliedFiles[f] = true
		}
		applied = append(applied, fix)
	}

	// Build commit message and create commit
	if len(applied) > 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "fix: ai-reviewed fixes for PR #%d\n", input.PRNumber)
		for _, fix := range applied {
			fmt.Fprintf(&b, "\n- %s", fix.CommitMsg)
		}
		commitMsg := b.String()

		if err := a.createCommitWithDiffs(ctx, input.RepoOwner, input.RepoName, branchName, headSHA, applied, commitMsg); err != nil {
			return types.CoalescedChangeset{}, fmt.Errorf("create commit: %w", err)
		}
	}

	return types.CoalescedChangeset{
		Applied:    applied,
		Conflicts:  conflicts,
		BranchName: branchName,
	}, nil
}

func (a *CoalesceActivity) getBranchSHA(ctx context.Context, owner, repo, branch string) (string, error) {
	ref, _, err := a.client.Git.GetRef(ctx, owner, repo, "heads/"+branch)
	if err != nil {
		return "", fmt.Errorf("get ref heads/%s: %w", branch, err)
	}
	return ref.GetObject().GetSHA(), nil
}

func (a *CoalesceActivity) createBranch(ctx context.Context, owner, repo, branch, sha string) error {
	_, _, err := a.client.Git.CreateRef(ctx, owner, repo, &github.Reference{
		Ref:    github.Ptr("refs/heads/" + branch),
		Object: &github.GitObject{SHA: github.Ptr(sha)},
	})
	if err != nil {
		return fmt.Errorf("create ref: %w", err)
	}
	return nil
}

func (a *CoalesceActivity) createCommitWithDiffs(ctx context.Context, owner, repo, branch, baseSHA string, fixes []types.FixResult, commitMsg string) error {
	// Build tree entries from fixes
	var entries []*github.TreeEntry

	for _, fix := range fixes {
		for _, filePath := range fix.FilesChanged {
			// Read current file content from the base
			fileContent, _, _, err := a.client.Repositories.GetContents(
				ctx, owner, repo, filePath,
				&github.RepositoryContentGetOptions{Ref: baseSHA},
			)
			if err != nil {
				return fmt.Errorf("read file %s: %w", filePath, err)
			}

			decoded, err := fileContent.GetContent()
			if err != nil {
				return fmt.Errorf("decode file %s: %w", filePath, err)
			}

			// Apply the diff to the file content (best-effort)
			newContent := applyDiffBestEffort(decoded, fix.Diff)

			entries = append(entries, &github.TreeEntry{
				Path:    github.Ptr(filePath),
				Mode:    github.Ptr("100644"),
				Type:    github.Ptr("blob"),
				Content: github.Ptr(newContent),
			})
		}
	}

	// Create tree
	tree, _, err := a.client.Git.CreateTree(ctx, owner, repo, baseSHA, entries)
	if err != nil {
		return fmt.Errorf("create tree: %w", err)
	}

	// Create commit
	commit, _, err := a.client.Git.CreateCommit(ctx, owner, repo, &github.Commit{
		Message: github.Ptr(commitMsg),
		Tree:    &github.Tree{SHA: tree.SHA},
		Parents: []*github.Commit{{SHA: github.Ptr(baseSHA)}},
	}, nil)
	if err != nil {
		return fmt.Errorf("create commit: %w", err)
	}

	// Update branch ref to point to new commit
	_, _, err = a.client.Git.UpdateRef(ctx, owner, repo, &github.Reference{
		Ref:    github.Ptr("refs/heads/" + branch),
		Object: &github.GitObject{SHA: commit.SHA},
	}, false)
	if err != nil {
		return fmt.Errorf("update ref: %w", err)
	}

	return nil
}

// applyDiffBestEffort is a best-effort diff application. In production, use a proper
// diff library. This simply returns the original content if the diff can't be applied.
func applyDiffBestEffort(original, diff string) string {
	lines := strings.Split(original, "\n")
	diffLines := strings.Split(diff, "\n")

	var result []string
	origIdx := 0

	for _, dl := range diffLines {
		if strings.HasPrefix(dl, "@@") {
			// Parse the hunk header to find the starting line in the original file.
			// Copy any unchanged lines between the previous hunk and this one first.
			if start, ok := parseHunkStart(dl); ok {
				for origIdx < start && origIdx < len(lines) {
					result = append(result, lines[origIdx])
					origIdx++
				}
			}
			continue
		}
		if strings.HasPrefix(dl, "---") || strings.HasPrefix(dl, "+++") {
			continue
		}
		if strings.HasPrefix(dl, "-") {
			origIdx++
			continue
		}
		if strings.HasPrefix(dl, "+") {
			result = append(result, dl[1:])
			continue
		}
		if strings.HasPrefix(dl, " ") {
			if origIdx < len(lines) {
				result = append(result, lines[origIdx])
				origIdx++
			}
			continue
		}
	}

	if len(result) == 0 {
		return original
	}

	for origIdx < len(lines) {
		result = append(result, lines[origIdx])
		origIdx++
	}

	return strings.Join(result, "\n")
}

// parseHunkStart parses the original-file starting line from a unified diff hunk header.
// For "@@ -45,6 +45,8 @@", it returns 44 (0-indexed). Returns (0, false) on parse failure.
func parseHunkStart(header string) (int, bool) {
	// Format: @@ -<start>[,<count>] +<start>[,<count>] @@
	parts := strings.Fields(header)
	if len(parts) < 3 || !strings.HasPrefix(parts[1], "-") {
		return 0, false
	}
	old := parts[1][1:] // strip leading "-"
	if idx := strings.IndexByte(old, ','); idx >= 0 {
		old = old[:idx]
	}
	n, err := strconv.Atoi(old)
	if err != nil || n < 1 {
		return 0, false
	}
	return n - 1, true // convert 1-indexed line number to 0-indexed slice position
}
