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
	"go.uber.org/zap"
)

const (
	// MaxDiffSizeBytes is the maximum size of a diff to fetch (10MB)
	MaxDiffSizeBytes = 10 * 1024 * 1024

	// MaxDiffChars is the maximum number of characters before truncation (50k)
	MaxDiffChars = 50000

	// MaxDiffLines is the maximum number of lines before truncation (1000)
	MaxDiffLines = 1000

	// DiffFetchTimeout is the timeout for HTTP requests
	DiffFetchTimeout = 10 * time.Second
)

// DiffFetcherActivity fetches and caches PR diffs.
// It uses the go-github SDK for GitHub PR diff URLs and falls back to
// a plain HTTP client for non-GitHub URLs (e.g. test servers).
type DiffFetcherActivity struct {
	ghClient   *github.Client
	httpClient *http.Client
	cache      *cache.DiffCache
	logger     *zap.Logger
}

// NewDiffFetcher creates a new DiffFetcherActivity.
// ghClient may be nil if no GitHub token is available; all fetches will use the HTTP fallback.
func NewDiffFetcher(logger *zap.Logger, ghClient *github.Client) *DiffFetcherActivity {
	return &DiffFetcherActivity{
		ghClient:   ghClient,
		httpClient: &http.Client{Timeout: DiffFetchTimeout},
		cache:      cache.NewDiffCache(),
		logger:     logger,
	}
}

// FetchDiff fetches a diff from URL with caching and size limits.
func (a *DiffFetcherActivity) FetchDiff(ctx context.Context, url string) (string, error) {
	// Check cache first
	if cached, found := a.cache.Get(url); found {
		a.logger.Info("Diff cache hit",
			zap.String("url", url),
			zap.Int("size", len(cached)))
		return cached, nil
	}

	a.logger.Info("Diff cache miss, fetching from URL",
		zap.String("url", url))

	start := time.Now()

	var content string
	var err error

	// Try the SDK path for GitHub PR diff URLs
	if owner, repo, number, ok := parsePRDiffURL(url); ok && a.ghClient != nil {
		content, err = a.fetchViaSDK(ctx, owner, repo, number)
	} else {
		content, err = a.fetchViaHTTP(ctx, url)
	}
	if err != nil {
		return "", err
	}

	originalSize := len(content)

	// Truncate if too large
	if len(content) > MaxDiffChars || countLines(content) > MaxDiffLines {
		content = truncateDiff(content)
		a.logger.Warn("Diff truncated due to size",
			zap.String("url", url),
			zap.Int("original_size", originalSize),
			zap.Int("truncated_size", len(content)))
	}

	duration := time.Since(start)

	// Cache the diff
	a.cache.Set(url, content)

	a.logger.Info("Diff fetched successfully",
		zap.String("url", url),
		zap.Int("size", len(content)),
		zap.Duration("fetch_duration", duration))

	return content, nil
}

// fetchViaSDK uses the go-github SDK to fetch a PR diff.
func (a *DiffFetcherActivity) fetchViaSDK(ctx context.Context, owner, repo string, number int) (string, error) {
	diff, _, err := a.ghClient.PullRequests.GetRaw(ctx, owner, repo, number, github.RawOptions{Type: github.Diff})
	if err != nil {
		a.logger.Error("Failed to fetch diff via SDK",
			zap.String("owner", owner),
			zap.String("repo", repo),
			zap.Int("number", number),
			zap.Error(err))
		return "", fmt.Errorf("fetch diff via SDK: %w", err)
	}
	return diff, nil
}

// fetchViaHTTP fetches a diff using a plain HTTP GET (fallback for non-GitHub URLs).
func (a *DiffFetcherActivity) fetchViaHTTP(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		a.logger.Error("Failed to fetch diff",
			zap.String("url", url),
			zap.Error(err))
		return "", fmt.Errorf("fetch diff: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		a.logger.Error("Diff fetch failed with non-200 status",
			zap.String("url", url),
			zap.Int("status", resp.StatusCode))
		return "", fmt.Errorf("fetch failed: status %d", resp.StatusCode)
	}

	// Read with size limit (10MB)
	limitedReader := io.LimitReader(resp.Body, MaxDiffSizeBytes)
	data, err := io.ReadAll(limitedReader)
	if err != nil {
		a.logger.Error("Failed to read diff",
			zap.String("url", url),
			zap.Error(err))
		return "", fmt.Errorf("read diff: %w", err)
	}

	return string(data), nil
}

// truncateDiff truncates a diff to the first 1000 lines or 50k characters
func truncateDiff(diff string) string {
	lines := strings.Split(diff, "\n")

	// Truncate by line count
	if len(lines) > MaxDiffLines {
		lines = lines[:MaxDiffLines]
	}

	truncated := strings.Join(lines, "\n")

	// Truncate by character count if still too large
	if len(truncated) > MaxDiffChars {
		truncated = truncated[:MaxDiffChars]
	}

	return truncated + "\n\n[... diff truncated ...]"
}

// parsePRDiffURL parses a GitHub PR diff URL into its components.
// Returns (owner, repo, number, true) for URLs like
// https://github.com/owner/repo/pull/42.diff, or ("", "", 0, false) otherwise.
func parsePRDiffURL(url string) (owner, repo string, number int, ok bool) {
	re := regexp.MustCompile(`^https://github\.com/([^/]+)/([^/]+)/pull/(\d+)\.diff$`)
	matches := re.FindStringSubmatch(url)
	if len(matches) != 4 {
		return "", "", 0, false
	}
	n, err := strconv.Atoi(matches[3])
	if err != nil {
		return "", "", 0, false
	}
	return matches[1], matches[2], n, true
}

// countLines counts the number of lines in a string
func countLines(s string) int {
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}
