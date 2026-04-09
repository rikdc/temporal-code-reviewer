package activities

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/go-github/v68/github"
	"github.com/rikdc/temporal-code-reviewer/config"
	"github.com/rikdc/temporal-code-reviewer/types"
	"go.uber.org/zap"
)

// ListPRsActivity lists open pull requests for a repository.
type ListPRsActivity struct {
	client  *github.Client
	logger  *zap.Logger
	filters config.PRFilters
}

// NewListPRsActivity creates a new ListPRsActivity.
func NewListPRsActivity(client *github.Client, logger *zap.Logger, filters config.PRFilters) *ListPRsActivity {
	return &ListPRsActivity{client: client, logger: logger, filters: filters}
}

// ListOpenPRs returns all open pull requests for the given "owner/repo" string.
// The repo name may be "*" (e.g. "my-org/*") to poll every repository owned by
// that owner. PRAuthor is populated; AutoFixEnabled is left false and set by
// the caller.
func (a *ListPRsActivity) ListOpenPRs(ctx context.Context, repo string) ([]types.PRReviewInput, error) {
	if a.client == nil {
		return nil, fmt.Errorf("GitHub client not configured: GITHUB_TOKEN is required")
	}

	owner, name, err := splitOwnerRepo(repo)
	if err != nil {
		return nil, err
	}

	if name == "*" {
		return a.listOpenPRsForOwner(ctx, owner)
	}

	return a.listOpenPRsForRepo(ctx, owner, name)
}

// listOpenPRsForOwner expands "owner/*" by first listing all repos for the
// owner, then collecting open PRs across all of them. It tries the org
// endpoint first and falls back to the user endpoint.
func (a *ListPRsActivity) listOpenPRsForOwner(ctx context.Context, owner string) ([]types.PRReviewInput, error) {
	repos, err := a.listRepos(ctx, owner)
	if err != nil {
		return nil, fmt.Errorf("list repos for %s: %w", owner, err)
	}

	a.logger.Info("Expanded wildcard repo pattern",
		zap.String("owner", owner),
		zap.Int("repo_count", len(repos)))

	var all []types.PRReviewInput
	for _, r := range repos {
		prs, err := a.listOpenPRsForRepo(ctx, owner, r.GetName())
		if err != nil {
			// Log and continue — one inaccessible repo shouldn't abort the rest.
			a.logger.Warn("Failed to list PRs for repo",
				zap.String("repo", owner+"/"+r.GetName()),
				zap.Error(err))
			continue
		}
		all = append(all, prs...)
	}

	return all, nil
}

// listRepos returns all repositories for the given owner, trying the org
// endpoint first and falling back to the user endpoint.
func (a *ListPRsActivity) listRepos(ctx context.Context, owner string) ([]*github.Repository, error) {
	orgOpts := &github.RepositoryListByOrgOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	}
	repos, _, err := a.client.Repositories.ListByOrg(ctx, owner, orgOpts)
	if err == nil {
		return repos, nil
	}

	// Not an org — try the user endpoint.
	userOpts := &github.RepositoryListByUserOptions{
		ListOptions: github.ListOptions{PerPage: 100},
	}
	repos, _, err = a.client.Repositories.ListByUser(ctx, owner, userOpts)
	if err != nil {
		return nil, err
	}
	return repos, nil
}

func (a *ListPRsActivity) listOpenPRsForRepo(ctx context.Context, owner, name string) ([]types.PRReviewInput, error) {
	opts := &github.PullRequestListOptions{
		State:       "open",
		ListOptions: github.ListOptions{PerPage: 100},
	}

	prs, _, err := a.client.PullRequests.List(ctx, owner, name, opts)
	if err != nil {
		return nil, fmt.Errorf("list PRs for %s/%s: %w", owner, name, err)
	}

	a.logger.Info("Listed open pull requests",
		zap.String("repo", owner+"/"+name),
		zap.Int("count", len(prs)))

	results := make([]types.PRReviewInput, 0, len(prs))
	for _, pr := range prs {
		if reason := a.filterReason(pr); reason != "" {
			a.logger.Info("Skipping PR",
				zap.String("repo", owner+"/"+name),
				zap.Int("pr_number", pr.GetNumber()),
				zap.String("reason", reason))
			continue
		}
		results = append(results, types.PRReviewInput{
			PRNumber:   pr.GetNumber(),
			RepoOwner:  owner,
			RepoName:   name,
			Title:      pr.GetTitle(),
			DiffURL:    pr.GetDiffURL(),
			HeadBranch: pr.GetHead().GetRef(),
			HeadSHA:    pr.GetHead().GetSHA(),
			BaseBranch: pr.GetBase().GetRef(),
			PRAuthor:   pr.GetUser().GetLogin(),
		})
	}

	return results, nil
}

// filterReason returns a non-empty string describing why the PR should be
// skipped, or an empty string if the PR passes all configured filters.
func (a *ListPRsActivity) filterReason(pr *github.PullRequest) string {
	f := a.filters

	if f.SkipDrafts && pr.GetDraft() {
		return "draft PR"
	}

	if f.SkipBots && pr.GetUser().GetType() == "Bot" {
		return fmt.Sprintf("bot author (%s)", pr.GetUser().GetLogin())
	}

	if f.MaxAgeDays > 0 {
		age := time.Since(pr.GetCreatedAt().Time)
		if age > time.Duration(f.MaxAgeDays)*24*time.Hour {
			return fmt.Sprintf("PR older than %d days (age: %.1fd)", f.MaxAgeDays, age.Hours()/24)
		}
	}

	if len(f.RequireReviewerLogins) > 0 {
		allowed := make(map[string]bool, len(f.RequireReviewerLogins))
		for _, login := range f.RequireReviewerLogins {
			allowed[login] = true
		}
		for _, reviewer := range pr.RequestedReviewers {
			if allowed[reviewer.GetLogin()] {
				return "" // matched — allow through
			}
		}
		return "none of the required reviewers are assigned"
	}

	return ""
}

func splitOwnerRepo(repo string) (owner, name string, err error) {
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("expected \"owner/repo\" or \"owner/*\", got %q", repo)
	}
	return parts[0], parts[1], nil
}
