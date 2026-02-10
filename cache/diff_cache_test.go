package cache

import (
	"testing"
	"time"
)

func TestNewDiffCache(t *testing.T) {
	cache := NewDiffCache()
	if cache == nil {
		t.Fatal("NewDiffCache() returned nil")
	}
	if cache.cache == nil {
		t.Fatal("NewDiffCache() cache is nil")
	}
	if cache.ItemCount() != 0 {
		t.Errorf("NewDiffCache() ItemCount = %d, want 0", cache.ItemCount())
	}
}

func TestDiffCache_SetAndGet(t *testing.T) {
	cache := NewDiffCache()

	tests := []struct {
		name        string
		diffURL     string
		diffContent string
	}{
		{
			name:        "simple diff",
			diffURL:     "https://github.com/owner/repo/pull/123.diff",
			diffContent: "diff --git a/file.go b/file.go\n+added line\n-removed line",
		},
		{
			name:        "large diff",
			diffURL:     "https://github.com/owner/repo/pull/456.diff",
			diffContent: string(make([]byte, 50000)), // 50KB diff
		},
		{
			name:        "empty diff",
			diffURL:     "https://github.com/owner/repo/pull/789.diff",
			diffContent: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set the diff
			cache.Set(tt.diffURL, tt.diffContent)

			// Get the diff back
			got, found := cache.Get(tt.diffURL)
			if !found {
				t.Errorf("Get(%q) found = false, want true", tt.diffURL)
				return
			}
			if got != tt.diffContent {
				t.Errorf("Get(%q) = %q, want %q", tt.diffURL, got, tt.diffContent)
			}
		})
	}
}

func TestDiffCache_GetMissing(t *testing.T) {
	cache := NewDiffCache()

	got, found := cache.Get("https://github.com/owner/repo/pull/999.diff")
	if found {
		t.Errorf("Get(missing key) found = true, want false")
	}
	if got != "" {
		t.Errorf("Get(missing key) = %q, want empty string", got)
	}
}

func TestDiffCache_SetWithTTL(t *testing.T) {
	cache := NewDiffCache()
	diffURL := "https://github.com/owner/repo/pull/100.diff"
	diffContent := "test diff content"

	// Set with short TTL
	cache.SetWithTTL(diffURL, diffContent, 100*time.Millisecond)

	// Should be present immediately
	got, found := cache.Get(diffURL)
	if !found {
		t.Error("Get() found = false immediately after SetWithTTL")
	}
	if got != diffContent {
		t.Errorf("Get() = %q, want %q", got, diffContent)
	}

	// Wait for expiration
	time.Sleep(150 * time.Millisecond)

	// Should be expired
	_, found = cache.Get(diffURL)
	if found {
		t.Error("Get() found = true after TTL expiration, want false")
	}
}

func TestDiffCache_Delete(t *testing.T) {
	cache := NewDiffCache()
	diffURL := "https://github.com/owner/repo/pull/200.diff"
	diffContent := "test diff content"

	// Set and verify
	cache.Set(diffURL, diffContent)
	if _, found := cache.Get(diffURL); !found {
		t.Error("Get() found = false after Set")
	}

	// Delete
	cache.Delete(diffURL)

	// Verify deleted
	if _, found := cache.Get(diffURL); found {
		t.Error("Get() found = true after Delete, want false")
	}
}

func TestDiffCache_Clear(t *testing.T) {
	cache := NewDiffCache()

	// Add multiple items
	cache.Set("url1", "content1")
	cache.Set("url2", "content2")
	cache.Set("url3", "content3")

	if count := cache.ItemCount(); count != 3 {
		t.Errorf("ItemCount() = %d, want 3", count)
	}

	// Clear cache
	cache.Clear()

	if count := cache.ItemCount(); count != 0 {
		t.Errorf("ItemCount() after Clear = %d, want 0", count)
	}

	// Verify items are gone
	if _, found := cache.Get("url1"); found {
		t.Error("Get(url1) found = true after Clear, want false")
	}
	if _, found := cache.Get("url2"); found {
		t.Error("Get(url2) found = true after Clear, want false")
	}
	if _, found := cache.Get("url3"); found {
		t.Error("Get(url3) found = true after Clear, want false")
	}
}

func TestDiffCache_ItemCount(t *testing.T) {
	cache := NewDiffCache()

	// Empty cache
	if count := cache.ItemCount(); count != 0 {
		t.Errorf("ItemCount() on empty cache = %d, want 0", count)
	}

	// Add items
	cache.Set("url1", "content1")
	if count := cache.ItemCount(); count != 1 {
		t.Errorf("ItemCount() after 1 Set = %d, want 1", count)
	}

	cache.Set("url2", "content2")
	if count := cache.ItemCount(); count != 2 {
		t.Errorf("ItemCount() after 2 Sets = %d, want 2", count)
	}

	// Overwrite existing
	cache.Set("url1", "new content")
	if count := cache.ItemCount(); count != 2 {
		t.Errorf("ItemCount() after overwrite = %d, want 2", count)
	}

	// Delete one
	cache.Delete("url1")
	if count := cache.ItemCount(); count != 1 {
		t.Errorf("ItemCount() after Delete = %d, want 1", count)
	}
}

func TestDiffCache_ConcurrentAccess(t *testing.T) {
	cache := NewDiffCache()
	done := make(chan bool)
	iterations := 100

	// Concurrent writes
	for i := 0; i < 10; i++ {
		go func(id int) {
			for j := 0; j < iterations; j++ {
				diffURL := "url" + string(rune(id))
				cache.Set(diffURL, "content")
				cache.Get(diffURL)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Cache should still be functional
	cache.Set("final", "test")
	if got, found := cache.Get("final"); !found || got != "test" {
		t.Error("Cache corrupted after concurrent access")
	}
}

func TestDiffCache_TTLExpiration(t *testing.T) {
	cache := NewDiffCache()
	diffURL := "https://github.com/owner/repo/pull/300.diff"
	diffContent := "test content"

	// Set with 5 minute default TTL (should not expire during test)
	cache.Set(diffURL, diffContent)

	// Verify it's there
	got, found := cache.Get(diffURL)
	if !found {
		t.Error("Get() found = false after Set")
	}
	if got != diffContent {
		t.Errorf("Get() = %q, want %q", got, diffContent)
	}

	// Should still be there after a short wait
	time.Sleep(50 * time.Millisecond)
	got, found = cache.Get(diffURL)
	if !found {
		t.Error("Get() found = false after 50ms (should not expire)")
	}
	if got != diffContent {
		t.Errorf("Get() = %q, want %q", got, diffContent)
	}
}
