package activities

import (
	"context"
	"fmt"

	"github.com/google/go-github/v68/github"
	"github.com/rikdc/temporal-code-reviewer/types"
	"go.uber.org/zap"
)

// GitHubActivity provides GitHub API activities using the go-github SDK.
type GitHubActivity struct {
	client *github.Client
	logger *zap.Logger
}

// NewGitHubActivity creates a new GitHubActivity.
func NewGitHubActivity(client *github.Client, logger *zap.Logger) *GitHubActivity {
	return &GitHubActivity{
		client: client,
		logger: logger,
	}
}

// GetPRHeadSHA returns the head commit SHA for a pull request.
// Use this when HeadSHA is not available from the webhook payload.
func (a *GitHubActivity) GetPRHeadSHA(ctx context.Context, owner, repo string, prNumber int) (string, error) {
	if a.client == nil {
		return "", fmt.Errorf("GitHub client not configured: GITHUB_TOKEN is required")
	}

	pr, _, err := a.client.PullRequests.Get(ctx, owner, repo, prNumber)
	if err != nil {
		return "", fmt.Errorf("get PR #%d: %w", prNumber, err)
	}

	sha := pr.GetHead().GetSHA()
	if sha == "" {
		return "", fmt.Errorf("PR #%d head SHA is empty", prNumber)
	}

	return sha, nil
}

// ReadFile fetches raw file content from the GitHub API.
func (a *GitHubActivity) ReadFile(ctx context.Context, input types.ReadFileInput) (string, error) {
	if a.client == nil {
		return "", fmt.Errorf("GitHub client not configured: GITHUB_TOKEN is required for file read operations")
	}

	if input.Ref == "" {
		return "", fmt.Errorf("ReadFile called with empty Ref for %s/%s:%s — HeadSHA must be set on PRReviewInput", input.RepoOwner, input.RepoName, input.FilePath)
	}

	a.logger.Info("Reading file from GitHub",
		zap.String("repo", input.RepoOwner+"/"+input.RepoName),
		zap.String("file", input.FilePath),
		zap.String("ref", input.Ref))

	fileContent, _, resp, err := a.client.Repositories.GetContents(
		ctx, input.RepoOwner, input.RepoName, input.FilePath,
		&github.RepositoryContentGetOptions{Ref: input.Ref},
	)
	if err != nil {
		return "", fmt.Errorf("github get contents: %w", err)
	}
	if resp != nil && resp.Body != nil {
		defer resp.Body.Close()
	}

	if fileContent == nil {
		return "", fmt.Errorf("path %s is a directory, not a file", input.FilePath)
	}

	content, err := fileContent.GetContent()
	if err != nil {
		return "", fmt.Errorf("decode file content: %w", err)
	}

	return content, nil
}
