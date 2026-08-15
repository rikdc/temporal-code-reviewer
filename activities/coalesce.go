package activities

import (
	"context"
	"fmt"
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

func NewCoalesceActivity(client *github.Client, logger *zap.Logger) *CoalesceActivity {
	return &CoalesceActivity{client: client, logger: logger}
}

func (a *CoalesceActivity) Execute(ctx context.Context, input types.CoalesceInput) (types.CoalescedChangeset, error) {
	if a.client == nil {
		return types.CoalescedChangeset{}, fmt.Errorf("GitHub client not configured: GITHUB_TOKEN is required for branch operations")
	}

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

	shortSHA := input.HeadSHA
	if len(shortSHA) > 8 {
		shortSHA = shortSHA[:8]
	}
	branchName := fmt.Sprintf("ai-fixes/pr-%d-%s", input.PRNumber, shortSHA)

	a.logger.Info("Coalescing fixes",
		zap.Int("successful_count", len(successful)),
		zap.String("branch", branchName),
		zap.String("base", input.HeadBranch))

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

	var applied []types.FixResult
	var conflicts []types.FixResult
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

	if len(applied) == 0 {
		return types.CoalescedChangeset{
			Conflicts: conflicts,
		}, nil
	}

	// Build tree entries from fixes with exact patch validation
	var entries []*github.TreeEntry
	for _, fix := range applied {
		for _, filePath := range fix.FilesChanged {
			// Read the file at the base SHA
			fileContent, _, _, err := a.client.Repositories.GetContents(
				ctx, input.RepoOwner, input.RepoName, filePath,
				&github.RepositoryContentGetOptions{Ref: headSHA},
			)
			if err != nil {
				return types.CoalescedChangeset{}, fmt.Errorf("read file %s: %w", filePath, err)
			}

			decoded, err := fileContent.GetContent()
			if err != nil {
				return types.CoalescedChangeset{}, fmt.Errorf("decode file %s: %w", filePath, err)
			}

			// Parse the patch
			patch, err := ParsePatch(fix.Diff)
			if err != nil {
				return types.CoalescedChangeset{}, fmt.Errorf("parse patch for %s: %w", filePath, err)
			}

			// Validate the patch targets the correct file
			if err := ValidatePatch(decoded, patch, filePath); err != nil {
				return types.CoalescedChangeset{}, fmt.Errorf("validate patch for %s: %w", filePath, err)
			}

			// Apply the patch with exact context matching
			newContent, err := ApplyPatch(decoded, patch)
			if err != nil {
				return types.CoalescedChangeset{}, fmt.Errorf("apply patch for %s: %w", filePath, err)
			}

			// Verify the result is different from the input
			if newContent == decoded {
				return types.CoalescedChangeset{}, fmt.Errorf("patch for %s produced no change", filePath)
			}

			entries = append(entries, &github.TreeEntry{
				Path:    github.Ptr(filePath),
				Mode:    github.Ptr("100644"),
				Type:    github.Ptr("blob"),
				Content: github.Ptr(newContent),
			})
		}
	}

	if len(entries) == 0 {
		return types.CoalescedChangeset{
			Applied:   applied,
			Conflicts: conflicts,
		}, nil
	}

	// Create tree first
	tree, _, err := a.client.Git.CreateTree(ctx, input.RepoOwner, input.RepoName, headSHA, entries)
	if err != nil {
		return types.CoalescedChangeset{}, fmt.Errorf("create tree: %w", err)
	}

	// Create commit before branch
	var commitMsg strings.Builder
	fmt.Fprintf(&commitMsg, "fix: ai-reviewed fixes for PR #%d\n", input.PRNumber)
	for _, fix := range applied {
		fmt.Fprintf(&commitMsg, "\n- %s", fix.CommitMsg)
	}

	commit, _, err := a.client.Git.CreateCommit(ctx, input.RepoOwner, input.RepoName, &github.Commit{
		Message: github.Ptr(commitMsg.String()),
		Tree:    &github.Tree{SHA: tree.SHA},
		Parents: []*github.Commit{{SHA: github.Ptr(headSHA)}},
	}, nil)
	if err != nil {
		return types.CoalescedChangeset{}, fmt.Errorf("create commit: %w", err)
	}

	// Create or update branch ref to point to the completed commit
	if existingSHA != "" {
		// Branch exists — update it
		_, _, err = a.client.Git.UpdateRef(ctx, input.RepoOwner, input.RepoName, &github.Reference{
			Ref:    github.Ptr("refs/heads/" + branchName),
			Object: &github.GitObject{SHA: commit.SHA},
		}, false)
		if err != nil {
			return types.CoalescedChangeset{}, fmt.Errorf("update ref: %w", err)
		}
	} else {
		// Create new branch
		_, _, err = a.client.Git.CreateRef(ctx, input.RepoOwner, input.RepoName, &github.Reference{
			Ref:    github.Ptr("refs/heads/" + branchName),
			Object: &github.GitObject{SHA: commit.SHA},
		})
		if err != nil {
			return types.CoalescedChangeset{}, fmt.Errorf("create ref: %w", err)
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
