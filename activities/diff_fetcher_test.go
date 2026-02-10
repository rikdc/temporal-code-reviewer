package activities

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"
)

func TestNewDiffFetcher(t *testing.T) {
	logger := zaptest.NewLogger(t)
	fetcher := NewDiffFetcher(logger)

	if fetcher == nil {
		t.Fatal("NewDiffFetcher() returned nil")
	}
	if fetcher.httpClient == nil {
		t.Error("NewDiffFetcher() httpClient is nil")
	}
	if fetcher.cache == nil {
		t.Error("NewDiffFetcher() cache is nil")
	}
	if fetcher.logger == nil {
		t.Error("NewDiffFetcher() logger is nil")
	}
	if fetcher.httpClient.Timeout != DiffFetchTimeout {
		t.Errorf("NewDiffFetcher() timeout = %v, want %v", fetcher.httpClient.Timeout, DiffFetchTimeout)
	}
}

func TestDiffFetcher_FetchDiff_Success(t *testing.T) {
	logger := zaptest.NewLogger(t)
	fetcher := NewDiffFetcher(logger)

	// Create test server
	diffContent := `diff --git a/file.go b/file.go
index 1234567..abcdefg 100644
--- a/file.go
+++ b/file.go
@@ -1,3 +1,4 @@
 package main
+import "fmt"
 func main() {
+	fmt.Println("Hello")
 }`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("Expected GET request, got %s", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(diffContent))
	}))
	defer server.Close()

	// Fetch diff
	ctx := context.Background()
	got, err := fetcher.FetchDiff(ctx, server.URL)
	if err != nil {
		t.Fatalf("FetchDiff() error = %v, want nil", err)
	}

	if got != diffContent {
		t.Errorf("FetchDiff() = %q, want %q", got, diffContent)
	}
}

func TestDiffFetcher_FetchDiff_CacheHit(t *testing.T) {
	logger := zaptest.NewLogger(t)
	fetcher := NewDiffFetcher(logger)

	diffContent := "test diff content"
	requestCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(diffContent))
	}))
	defer server.Close()

	ctx := context.Background()

	// First fetch - should hit server
	got1, err := fetcher.FetchDiff(ctx, server.URL)
	if err != nil {
		t.Fatalf("First FetchDiff() error = %v", err)
	}
	if got1 != diffContent {
		t.Errorf("First FetchDiff() = %q, want %q", got1, diffContent)
	}
	if requestCount != 1 {
		t.Errorf("First fetch requestCount = %d, want 1", requestCount)
	}

	// Second fetch - should hit cache
	got2, err := fetcher.FetchDiff(ctx, server.URL)
	if err != nil {
		t.Fatalf("Second FetchDiff() error = %v", err)
	}
	if got2 != diffContent {
		t.Errorf("Second FetchDiff() = %q, want %q", got2, diffContent)
	}
	if requestCount != 1 {
		t.Errorf("Second fetch requestCount = %d, want 1 (should be cached)", requestCount)
	}
}

func TestDiffFetcher_FetchDiff_TruncateByLines(t *testing.T) {
	logger := zaptest.NewLogger(t)
	fetcher := NewDiffFetcher(logger)

	// Create diff with >1000 lines
	var lines []string
	for i := 0; i < 1500; i++ {
		lines = append(lines, "+line "+string(rune('0'+i%10)))
	}
	largeDiff := strings.Join(lines, "\n")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(largeDiff))
	}))
	defer server.Close()

	ctx := context.Background()
	got, err := fetcher.FetchDiff(ctx, server.URL)
	if err != nil {
		t.Fatalf("FetchDiff() error = %v", err)
	}

	// Should be truncated to 1000 lines
	gotLines := strings.Split(got, "\n")
	if len(gotLines) > MaxDiffLines+5 { // Allow some margin for truncation message
		t.Errorf("FetchDiff() returned %d lines, want <= %d", len(gotLines), MaxDiffLines+5)
	}

	if !strings.Contains(got, "[... diff truncated ...]") {
		t.Error("FetchDiff() should contain truncation message")
	}
}

func TestDiffFetcher_FetchDiff_TruncateByChars(t *testing.T) {
	logger := zaptest.NewLogger(t)
	fetcher := NewDiffFetcher(logger)

	// Create diff with >50k characters but <1000 lines
	// Each line is about 100 chars, so 800 lines * 100 = 80k chars
	var lines []string
	for i := 0; i < 800; i++ {
		lines = append(lines, "+"+strings.Repeat("a", 99))
	}
	largeDiff := strings.Join(lines, "\n")

	if len(largeDiff) <= MaxDiffChars {
		t.Fatalf("Test setup error: diff size %d should be > %d", len(largeDiff), MaxDiffChars)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(largeDiff))
	}))
	defer server.Close()

	ctx := context.Background()
	got, err := fetcher.FetchDiff(ctx, server.URL)
	if err != nil {
		t.Fatalf("FetchDiff() error = %v", err)
	}

	// Should be truncated to ~50k chars
	if len(got) > MaxDiffChars+100 { // Allow some margin for truncation message
		t.Errorf("FetchDiff() returned %d chars, want <= %d", len(got), MaxDiffChars+100)
	}

	if !strings.Contains(got, "[... diff truncated ...]") {
		t.Error("FetchDiff() should contain truncation message")
	}
}

func TestDiffFetcher_FetchDiff_SizeLimit(t *testing.T) {
	logger := zaptest.NewLogger(t)
	fetcher := NewDiffFetcher(logger)

	// Create diff exactly at size limit (10MB)
	// Note: LimitReader will cap it, so we just verify it doesn't error
	largeDiff := strings.Repeat("a", MaxDiffSizeBytes)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(largeDiff))
	}))
	defer server.Close()

	ctx := context.Background()
	got, err := fetcher.FetchDiff(ctx, server.URL)
	if err != nil {
		t.Fatalf("FetchDiff() error = %v", err)
	}

	// Should be limited to 10MB and then truncated by char count
	if len(got) > MaxDiffSizeBytes {
		t.Errorf("FetchDiff() returned %d bytes, want <= %d", len(got), MaxDiffSizeBytes)
	}

	// Will also be truncated by character count (50k)
	if len(got) > MaxDiffChars+100 {
		t.Errorf("FetchDiff() returned %d chars after truncation, want <= %d", len(got), MaxDiffChars+100)
	}
}

func TestDiffFetcher_FetchDiff_HTTPError_404(t *testing.T) {
	logger := zaptest.NewLogger(t)
	fetcher := NewDiffFetcher(logger)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("Not Found"))
	}))
	defer server.Close()

	ctx := context.Background()
	_, err := fetcher.FetchDiff(ctx, server.URL)
	if err == nil {
		t.Error("FetchDiff() with 404 should return error")
	}
	if !strings.Contains(err.Error(), "status 404") {
		t.Errorf("FetchDiff() error = %q, want to contain 'status 404'", err.Error())
	}
}

func TestDiffFetcher_FetchDiff_HTTPError_500(t *testing.T) {
	logger := zaptest.NewLogger(t)
	fetcher := NewDiffFetcher(logger)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
	}))
	defer server.Close()

	ctx := context.Background()
	_, err := fetcher.FetchDiff(ctx, server.URL)
	if err == nil {
		t.Error("FetchDiff() with 500 should return error")
	}
	if !strings.Contains(err.Error(), "status 500") {
		t.Errorf("FetchDiff() error = %q, want to contain 'status 500'", err.Error())
	}
}

func TestDiffFetcher_FetchDiff_Timeout(t *testing.T) {
	logger := zaptest.NewLogger(t)
	fetcher := NewDiffFetcher(logger)

	// Create server with delay
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("delayed response"))
	}))
	defer server.Close()

	// Create context with short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := fetcher.FetchDiff(ctx, server.URL)
	if err == nil {
		t.Error("FetchDiff() with timeout should return error")
	}
	if !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Errorf("FetchDiff() error = %q, want context deadline exceeded", err.Error())
	}
}

func TestDiffFetcher_FetchDiff_InvalidURL(t *testing.T) {
	logger := zaptest.NewLogger(t)
	fetcher := NewDiffFetcher(logger)

	ctx := context.Background()
	_, err := fetcher.FetchDiff(ctx, "http://nonexistent-domain-12345.invalid")
	if err == nil {
		t.Error("FetchDiff() with invalid URL should return error")
	}
}

func TestDiffFetcher_FetchDiff_EmptyDiff(t *testing.T) {
	logger := zaptest.NewLogger(t)
	fetcher := NewDiffFetcher(logger)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		// Empty response
	}))
	defer server.Close()

	ctx := context.Background()
	got, err := fetcher.FetchDiff(ctx, server.URL)
	if err != nil {
		t.Fatalf("FetchDiff() error = %v", err)
	}

	if got != "" {
		t.Errorf("FetchDiff() = %q, want empty string", got)
	}
}

func TestTruncateDiff(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantLines int
		wantChars int
	}{
		{
			name:      "small diff - no truncation",
			input:     strings.Repeat("line\n", 10),
			wantLines: 10,
			wantChars: 50,
		},
		{
			name:      "large line count",
			input:     strings.Repeat("line\n", 1500),
			wantLines: MaxDiffLines,
			wantChars: -1, // Don't check exact chars, just verify truncation
		},
		{
			name:      "large character count",
			input:     strings.Repeat("a", 60000),
			wantLines: 1, // Single long line
			wantChars: MaxDiffChars,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateDiff(tt.input)

			gotLines := strings.Count(got, "\n") + 1
			if tt.wantLines > 0 && gotLines > tt.wantLines+5 { // Allow margin for truncation message
				t.Errorf("truncateDiff() line count = %d, want <= %d", gotLines, tt.wantLines+5)
			}

			if tt.wantChars > 0 && len(got) > tt.wantChars+100 { // Allow margin for truncation message
				t.Errorf("truncateDiff() char count = %d, want <= %d", len(got), tt.wantChars+100)
			}

			// Large diffs should have truncation message
			if len(tt.input) > MaxDiffChars || strings.Count(tt.input, "\n")+1 > MaxDiffLines {
				if !strings.Contains(got, "[... diff truncated ...]") {
					t.Error("truncateDiff() should contain truncation message")
				}
			}
		})
	}
}

func TestCountLines(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int
	}{
		{
			name:  "empty string",
			input: "",
			want:  0,
		},
		{
			name:  "single line",
			input: "line1",
			want:  1,
		},
		{
			name:  "two lines",
			input: "line1\nline2",
			want:  2,
		},
		{
			name:  "trailing newline",
			input: "line1\nline2\n",
			want:  3,
		},
		{
			name:  "multiple newlines",
			input: "line1\n\n\nline2",
			want:  4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := countLines(tt.input)
			if got != tt.want {
				t.Errorf("countLines() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestDiffFetcher_FetchDiff_MultipleURLs(t *testing.T) {
	logger := zaptest.NewLogger(t)
	fetcher := NewDiffFetcher(logger)

	// Test caching works correctly for different URLs
	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("diff1"))
	}))
	defer server1.Close()

	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("diff2"))
	}))
	defer server2.Close()

	ctx := context.Background()

	// Fetch from first URL
	got1, err := fetcher.FetchDiff(ctx, server1.URL)
	if err != nil {
		t.Fatalf("FetchDiff(url1) error = %v", err)
	}
	if got1 != "diff1" {
		t.Errorf("FetchDiff(url1) = %q, want 'diff1'", got1)
	}

	// Fetch from second URL
	got2, err := fetcher.FetchDiff(ctx, server2.URL)
	if err != nil {
		t.Fatalf("FetchDiff(url2) error = %v", err)
	}
	if got2 != "diff2" {
		t.Errorf("FetchDiff(url2) = %q, want 'diff2'", got2)
	}

	// Fetch from first URL again - should be cached
	got3, err := fetcher.FetchDiff(ctx, server1.URL)
	if err != nil {
		t.Fatalf("FetchDiff(url1 cached) error = %v", err)
	}
	if got3 != "diff1" {
		t.Errorf("FetchDiff(url1 cached) = %q, want 'diff1'", got3)
	}
}
