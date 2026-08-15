package activities

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/go-github/v68/github"
	"github.com/rikdc/temporal-code-reviewer/cache"
	"github.com/rikdc/temporal-code-reviewer/types"
	"go.uber.org/zap"
)

const (
	MaxDiffSizeBytes = 10 * 1024 * 1024
	MaxDiffChars     = 50000
	MaxDiffLines     = 1000
	DiffFetchTimeout = 10 * time.Second
)

// DiffFetcherActivity fetches and caches PR diffs.
type DiffFetcherActivity struct {
	ghClient   *github.Client
	httpClient *http.Client
	cache      *cache.DiffCache
	logger     *zap.Logger
}

// DiffFetchResult contains the diff content and coverage metadata.
type DiffFetchResult struct {
	Content  string            `json:"content"`
	Coverage types.DiffCoverage `json:"coverage"`
}

func NewDiffFetcher(logger *zap.Logger, ghClient *github.Client) *DiffFetcherActivity {
	return &DiffFetcherActivity{
		ghClient:   ghClient,
		httpClient: &http.Client{Timeout: DiffFetchTimeout},
		cache:      cache.NewDiffCache(),
		logger:     logger,
	}
}

// FetchDiff fetches a diff from URL with caching, size limits, and coverage tracking.
func (a *DiffFetcherActivity) FetchDiff(ctx context.Context, input DiffFetchInput) (DiffFetchResult, error) {
	url := input.DiffURL

	// Check cache first
	if cached, found := a.cache.Get(url); found {
		a.logger.Info("Diff cache hit", zap.String("url", url), zap.Int("size", len(cached)))
		return DiffFetchResult{Content: cached, Coverage: types.DiffCoverage{Truncated: false}}, nil
	}

	a.logger.Info("Diff cache miss, fetching from URL", zap.String("url", url))
	start := time.Now()

	var content string
	var err error

	if owner, repo, number, ok := parsePRDiffURL(url); ok && a.ghClient != nil {
		content, err = a.fetchViaSDK(ctx, owner, repo, number)
	} else {
		content, err = a.fetchViaHTTP(ctx, url)
	}
	if err != nil {
		return DiffFetchResult{}, err
	}

	originalSize := len(content)
	originalLines := countLines(content)

	coverage := types.DiffCoverage{
		TotalDiffBytes: originalSize,
		TotalDiffLines: originalLines,
	}

	// Truncate if too large
	if len(content) > MaxDiffChars || countLines(content) > MaxDiffLines {
		content = truncateDiff(content)
		coverage.Truncated = true
		coverage.TruncatedAtBytes = len(content)
		coverage.TruncatedAtLines = countLines(content)
		coverage.OmissionReason = fmt.Sprintf("diff exceeded %d chars or %d lines", MaxDiffChars, MaxDiffLines)
		coverage.OmittedBytes = originalSize - len(content)
		coverage.OmittedLines = originalLines - countLines(content)
		a.logger.Warn("Diff truncated due to size",
			zap.String("url", url),
			zap.Int("original_size", originalSize),
			zap.Int("truncated_size", len(content)))
	}

	coverage.ReviewedBytes = len(content)
	coverage.ReviewedLines = countLines(content)

	a.cache.Set(url, content)
	a.logger.Info("Diff fetched successfully",
		zap.String("url", url),
		zap.Int("size", len(content)),
		zap.Duration("fetch_duration", time.Since(start)))

	return DiffFetchResult{Content: content, Coverage: coverage}, nil
}

// DiffFetchInput wraps the diff URL with additional context.
type DiffFetchInput struct {
	DiffURL   string `json:"diff_url"`
	RepoOwner string `json:"repo_owner"`
	RepoName  string `json:"repo_name"`
	PRNumber  int    `json:"pr_number"`
}

func (a *DiffFetcherActivity) fetchViaSDK(ctx context.Context, owner, repo string, number int) (string, error) {
	diff, _, err := a.ghClient.PullRequests.GetRaw(ctx, owner, repo, number, github.RawOptions{Type: github.Diff})
	if err != nil {
		a.logger.Error("Failed to fetch diff via SDK",
			zap.String("owner", owner), zap.String("repo", repo),
			zap.Int("number", number), zap.Error(err))
		return "", fmt.Errorf("fetch diff via SDK: %w", err)
	}
	return diff, nil
}

func (a *DiffFetcherActivity) fetchViaHTTP(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		a.logger.Error("Failed to fetch diff", zap.String("url", url), zap.Error(err))
		return "", fmt.Errorf("fetch diff: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch failed: status %d", resp.StatusCode)
	}
	limitedReader := io.LimitReader(resp.Body, MaxDiffSizeBytes)
	data, err := io.ReadAll(limitedReader)
	if err != nil {
		return "", fmt.Errorf("read diff: %w", err)
	}
	return string(data), nil
}

func truncateDiff(diff string) string {
	lines := strings.Split(diff, "\n")
	if len(lines) > MaxDiffLines {
		lines = lines[:MaxDiffLines]
	}
	truncated := strings.Join(lines, "\n")
	if len(truncated) > MaxDiffChars {
		truncated = truncated[:MaxDiffChars]
	}
	return truncated + "\n\n[... diff truncated ...]"
}

var prDiffURLRe = regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+)/pull/(\d+)\.diff$`)

func parsePRDiffURL(url string) (owner, repo string, number int, ok bool) {
	matches := prDiffURLRe.FindStringSubmatch(url)
	if len(matches) != 4 {
		return "", "", 0, false
	}
	n, err := strconv.Atoi(matches[3])
	if err != nil {
		return "", "", 0, false
	}
	return matches[1], matches[2], n, true
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}
