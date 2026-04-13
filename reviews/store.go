package reviews

import (
	"fmt"
	"sync"
	"time"

	"github.com/rikdc/temporal-code-reviewer/types"
)

// State constants for a posted review.
const (
	StatePending   = "pending"   // draft review posted to GitHub; awaiting human submission
	StateSubmitted = "submitted" // review submitted on GitHub
)

// Record holds the persisted state of one posted PR review.
type Record struct {
	ID        string    `json:"id"`         // "owner/repo#pr@sha"
	RepoOwner string    `json:"repo_owner"`
	RepoName  string    `json:"repo_name"`
	PRNumber  int       `json:"pr_number"`
	Title     string    `json:"title"`
	PRAuthor  string    `json:"pr_author"`
	HeadSHA   string    `json:"head_sha"`
	PRURL     string    `json:"pr_url"`
	State     string    `json:"state"`
	PostedAt  time.Time `json:"posted_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Store is a thread-safe in-memory store for posted review records.
// New records are fanned out to SSE subscribers immediately.
type Store struct {
	mu          sync.RWMutex
	records     map[string]*Record // keyed by Record.ID
	ordered     []string           // insertion order; used by List
	subscribers []chan Record
}

// NewStore creates an empty Store.
func NewStore() *Store {
	return &Store{
		records: make(map[string]*Record),
	}
}

// Add records a newly posted review and notifies all current SSE subscribers.
// If a record with the same ID already exists it is overwritten (re-review
// after a new commit push).
func (s *Store) Add(input types.PostReviewInput) Record {
	pr := input.PRReviewInput
	id := fmt.Sprintf("%s/%s#%d@%s", pr.RepoOwner, pr.RepoName, pr.PRNumber, pr.HeadSHA)
	prURL := fmt.Sprintf("https://github.com/%s/%s/pull/%d", pr.RepoOwner, pr.RepoName, pr.PRNumber)
	now := time.Now()

	rec := Record{
		ID:        id,
		RepoOwner: pr.RepoOwner,
		RepoName:  pr.RepoName,
		PRNumber:  pr.PRNumber,
		Title:     pr.Title,
		PRAuthor:  pr.PRAuthor,
		HeadSHA:   pr.HeadSHA,
		PRURL:     prURL,
		State:     StatePending,
		PostedAt:  now,
		UpdatedAt: now,
	}

	s.mu.Lock()
	if _, exists := s.records[id]; !exists {
		s.ordered = append(s.ordered, id)
	}
	s.records[id] = &rec
	subs := make([]chan Record, len(s.subscribers))
	copy(subs, s.subscribers)
	s.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- rec:
		default:
			// Subscriber is full; drop rather than block.
		}
	}

	return rec
}

// List returns all records in insertion order (oldest first).
func (s *Store) List() []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Record, 0, len(s.ordered))
	for _, id := range s.ordered {
		out = append(out, *s.records[id])
	}
	return out
}

// Subscribe returns a buffered channel that receives every new Record.
func (s *Store) Subscribe() chan Record {
	ch := make(chan Record, 64)
	s.mu.Lock()
	s.subscribers = append(s.subscribers, ch)
	s.mu.Unlock()
	return ch
}

// Unsubscribe removes a subscriber and closes its channel.
func (s *Store) Unsubscribe(ch chan Record) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for i, sub := range s.subscribers {
		if sub == ch {
			s.subscribers = append(s.subscribers[:i], s.subscribers[i+1:]...)
			close(ch)
			return
		}
	}
}
