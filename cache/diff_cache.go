package cache

import (
	"time"

	gocache "github.com/patrickmn/go-cache"
)

const (
	// DefaultTTL is the default time-to-live for cached diffs (5 minutes)
	DefaultTTL = 5 * time.Minute

	// DefaultCleanupInterval is how often expired items are purged (10 minutes)
	DefaultCleanupInterval = 10 * time.Minute
)

// DiffCache provides thread-safe in-memory caching for PR diffs
type DiffCache struct {
	cache *gocache.Cache
}

// NewDiffCache creates a new DiffCache with default TTL and cleanup interval
func NewDiffCache() *DiffCache {
	return &DiffCache{
		cache: gocache.New(DefaultTTL, DefaultCleanupInterval),
	}
}

// Get retrieves a diff from the cache by diff URL
// Returns the diff content and true if found, empty string and false if not found
func (dc *DiffCache) Get(diffURL string) (string, bool) {
	if value, found := dc.cache.Get(diffURL); found {
		if diff, ok := value.(string); ok {
			return diff, true
		}
	}
	return "", false
}

// Set stores a diff in the cache with the default TTL
func (dc *DiffCache) Set(diffURL string, diffContent string) {
	dc.cache.Set(diffURL, diffContent, gocache.DefaultExpiration)
}

// SetWithTTL stores a diff in the cache with a custom TTL
func (dc *DiffCache) SetWithTTL(diffURL string, diffContent string, ttl time.Duration) {
	dc.cache.Set(diffURL, diffContent, ttl)
}

// Delete removes a diff from the cache
func (dc *DiffCache) Delete(diffURL string) {
	dc.cache.Delete(diffURL)
}

// Clear removes all diffs from the cache
func (dc *DiffCache) Clear() {
	dc.cache.Flush()
}

// ItemCount returns the number of items in the cache
func (dc *DiffCache) ItemCount() int {
	return dc.cache.ItemCount()
}
