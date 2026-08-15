package reviews

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rikdc/temporal-code-reviewer/types"
)

const (
	StatePending   = "pending"
	StateSubmitted = "submitted"
	StateClosed    = "closed"
)

type Record struct {
	ID             string    `json:"id"`
	RepoOwner      string    `json:"repo_owner"`
	RepoName       string    `json:"repo_name"`
	PRNumber       int       `json:"pr_number"`
	Title          string    `json:"title"`
	PRAuthor       string    `json:"pr_author"`
	HeadSHA        string    `json:"head_sha"`
	PRURL          string    `json:"pr_url"`
	State          string    `json:"state"`
	PostedAt       time.Time `json:"posted_at"`
	UpdatedAt      time.Time `json:"updated_at"`
	GitHubReviewID int64     `json:"github_review_id,omitempty"`
	ReviewBody     string    `json:"review_body,omitempty"`
}

// ReviewRecord is a review record for SQLite persistence.
type ReviewRecord struct {
	ID             string
	RepoOwner      string
	RepoName       string
	PRNumber       int
	Title          string
	PRAuthor       string
	HeadSHA        string
	PRURL          string
	State          string
	PostedAt       time.Time
	UpdatedAt      time.Time
	GitHubReviewID int64
	ReviewBody     string
}

// ReviewPersister abstracts SQLite persistence for review records.
type ReviewPersister interface {
	SaveReviewRecord(ctx context.Context, id, repoOwner, repoName string, prNumber int, title, prAuthor, headSHA, prURL, state, reviewBody string, githubReviewID int64, postedAt, updatedAt time.Time) error
	ListReviewRecords(ctx context.Context) ([]ReviewRecord, error)
	UpdateReviewRecordState(ctx context.Context, repoOwner, repoName string, prNumber int, state string) error
}

// Store is a thread-safe store for posted review records.
// Writes are persisted to SQLite when a persister is configured.
type Store struct {
	mu          sync.RWMutex
	records     map[string]*Record
	ordered     []string
	subscribers []chan Record
	persister   ReviewPersister
}

func NewStore() *Store {
	return &Store{records: make(map[string]*Record)}
}

// NewStoreWithPersistence creates a Store backed by SQLite.
func NewStoreWithPersistence(persister ReviewPersister) *Store {
	s := &Store{
		records:   make(map[string]*Record),
		persister: persister,
	}
	s.loadFromDB()
	return s
}

func (s *Store) loadFromDB() {
	if s.persister == nil {
		return
	}

	records, err := s.persister.ListReviewRecords(context.Background())
	if err != nil {
		return
	}

	for _, r := range records {
		rec := &Record{
			ID:             r.ID,
			RepoOwner:      r.RepoOwner,
			RepoName:       r.RepoName,
			PRNumber:       r.PRNumber,
			Title:          r.Title,
			PRAuthor:       r.PRAuthor,
			HeadSHA:        r.HeadSHA,
			PRURL:          r.PRURL,
			State:          r.State,
			PostedAt:       r.PostedAt,
			UpdatedAt:      r.UpdatedAt,
			GitHubReviewID: r.GitHubReviewID,
			ReviewBody:     r.ReviewBody,
		}
		s.records[rec.ID] = rec
		s.ordered = append(s.ordered, rec.ID)
	}
}

func (s *Store) Add(input types.PostReviewInput, githubReviewID int64, reviewBody string) Record {
	pr := input.PRReviewInput
	id := fmt.Sprintf("%s/%s#%d@%s", pr.RepoOwner, pr.RepoName, pr.PRNumber, pr.HeadSHA)
	prURL := fmt.Sprintf("https://github.com/%s/%s/pull/%d", pr.RepoOwner, pr.RepoName, pr.PRNumber)
	now := time.Now()

	rec := Record{
		ID:             id,
		RepoOwner:      pr.RepoOwner,
		RepoName:       pr.RepoName,
		PRNumber:       pr.PRNumber,
		Title:          pr.Title,
		PRAuthor:       pr.PRAuthor,
		HeadSHA:        pr.HeadSHA,
		PRURL:          prURL,
		State:          StatePending,
		PostedAt:       now,
		UpdatedAt:      now,
		GitHubReviewID: githubReviewID,
		ReviewBody:     reviewBody,
	}

	s.mu.Lock()
	if _, exists := s.records[id]; !exists {
		s.ordered = append(s.ordered, id)
	}
	s.records[id] = &rec
	subs := make([]chan Record, len(s.subscribers))
	copy(subs, s.subscribers)
	s.mu.Unlock()

	// Persist to SQLite
	if s.persister != nil {
		_ = s.persister.SaveReviewRecord(context.Background(),
			id, pr.RepoOwner, pr.RepoName, pr.PRNumber, pr.Title, pr.PRAuthor,
			pr.HeadSHA, prURL, StatePending, reviewBody, githubReviewID, now, now)
	}

	for _, ch := range subs {
		select {
		case ch <- rec:
		default:
		}
	}

	return rec
}

func (s *Store) List() []Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Record, 0, len(s.ordered))
	for _, id := range s.ordered {
		out = append(out, *s.records[id])
	}
	return out
}

func (s *Store) Subscribe() chan Record {
	ch := make(chan Record, 64)
	s.mu.Lock()
	s.subscribers = append(s.subscribers, ch)
	s.mu.Unlock()
	return ch
}

func (s *Store) FindPendingByPR(repoOwner, repoName string, prNumber int) *Record {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := len(s.ordered) - 1; i >= 0; i-- {
		rec := s.records[s.ordered[i]]
		if rec.RepoOwner == repoOwner && rec.RepoName == repoName && rec.PRNumber == prNumber && rec.State == StatePending {
			copy := *rec
			return &copy
		}
	}
	return nil
}

func (s *Store) MarkSubmitted(repoOwner, repoName string, prNumber int) {
	now := time.Now()
	s.mu.Lock()
	var updated []Record
	for _, id := range s.ordered {
		rec := s.records[id]
		if rec.RepoOwner == repoOwner && rec.RepoName == repoName && rec.PRNumber == prNumber && rec.State == StatePending {
			rec.State = StateSubmitted
			rec.UpdatedAt = now
			updated = append(updated, *rec)
		}
	}
	subs := make([]chan Record, len(s.subscribers))
	copy(subs, s.subscribers)
	s.mu.Unlock()

	if s.persister != nil {
		_ = s.persister.UpdateReviewRecordState(context.Background(), repoOwner, repoName, prNumber, StateSubmitted)
	}

	for _, rec := range updated {
		for _, ch := range subs {
			select {
			case ch <- rec:
			default:
			}
		}
	}
}

func (s *Store) MarkClosed(repoOwner, repoName string, prNumber int) {
	now := time.Now()
	s.mu.Lock()
	var updated []Record
	for _, id := range s.ordered {
		rec := s.records[id]
		if rec.RepoOwner == repoOwner && rec.RepoName == repoName && rec.PRNumber == prNumber {
			rec.State = StateClosed
			rec.UpdatedAt = now
			updated = append(updated, *rec)
		}
	}
	subs := make([]chan Record, len(s.subscribers))
	copy(subs, s.subscribers)
	s.mu.Unlock()

	if s.persister != nil {
		_ = s.persister.UpdateReviewRecordState(context.Background(), repoOwner, repoName, prNumber, StateClosed)
	}

	for _, rec := range updated {
		for _, ch := range subs {
			select {
			case ch <- rec:
			default:
			}
		}
	}
}

func (s *Store) Unsubscribe(ch chan Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, sub := range s.subscribers {
		if sub == ch {
			s.subscribers = append(s.subscribers[:i], s.subscribers[i+1:]...)
			return
		}
	}
}
