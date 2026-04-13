package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/rikdc/temporal-code-reviewer/metrics"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS prompt_versions (
	id         TEXT PRIMARY KEY,
	agent_name TEXT NOT NULL,
	label      TEXT NOT NULL,
	content    TEXT NOT NULL,
	disabled   INTEGER NOT NULL DEFAULT 0,
	created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS review_runs (
	id               TEXT PRIMARY KEY,
	pr_number        INTEGER NOT NULL,
	repo_owner       TEXT NOT NULL,
	repo_name        TEXT NOT NULL,
	head_sha         TEXT NOT NULL,
	github_review_id INTEGER NOT NULL DEFAULT 0,
	created_at       DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS agent_runs (
	id                TEXT PRIMARY KEY,
	review_run_id     TEXT NOT NULL,
	agent_name        TEXT NOT NULL,
	status            TEXT NOT NULL,
	model             TEXT NOT NULL,
	input_tokens      INTEGER NOT NULL DEFAULT 0,
	output_tokens     INTEGER NOT NULL DEFAULT 0,
	latency_ms        INTEGER NOT NULL DEFAULT 0,
	findings_count    INTEGER NOT NULL DEFAULT 0,
	prompt_version_id TEXT,
	created_at        DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS findings (
	id                TEXT PRIMARY KEY,
	agent_run_id      TEXT NOT NULL,
	severity          TEXT NOT NULL,
	title             TEXT NOT NULL,
	file_path         TEXT NOT NULL DEFAULT '',
	line_number       INTEGER NOT NULL DEFAULT 0,
	github_comment_id INTEGER NOT NULL DEFAULT 0,
	created_at        DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS feedback_events (
	id         TEXT PRIMARY KEY,
	finding_id TEXT NOT NULL,
	verdict    TEXT NOT NULL,
	source     TEXT NOT NULL,
	created_at DATETIME NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS pr_skips (
	repo_owner TEXT    NOT NULL,
	repo_name  TEXT    NOT NULL,
	pr_number  INTEGER NOT NULL,
	head_sha   TEXT    NOT NULL,
	created_at DATETIME NOT NULL DEFAULT (datetime('now')),
	PRIMARY KEY (repo_owner, repo_name, pr_number, head_sha)
);

CREATE INDEX IF NOT EXISTS idx_agent_runs_review ON agent_runs(review_run_id);
CREATE INDEX IF NOT EXISTS idx_findings_agent_run ON findings(agent_run_id);
CREATE INDEX IF NOT EXISTS idx_findings_comment ON findings(github_comment_id);
CREATE INDEX IF NOT EXISTS idx_feedback_finding ON feedback_events(finding_id);
CREATE INDEX IF NOT EXISTS idx_prompt_versions_agent ON prompt_versions(agent_name, disabled);
`

// Store is the SQLite implementation of metrics.Repository.
type Store struct {
	db *sql.DB
}

// Open opens (or creates) the SQLite database at the given path, applying the
// schema. The directory is created if it does not exist.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create metrics dir: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open metrics db: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite write serialisation
	if _, err := db.ExecContext(context.Background(), schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error { return s.db.Close() }

// ── Prompt versions ──────────────────────────────────────────────────────────

// SeedPrompt inserts an initial prompt version for an agent if none exist yet.
// It is idempotent — the INSERT is conditional so concurrent callers are safe.
func (s *Store) SeedPrompt(ctx context.Context, agentName, label, content string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO prompt_versions (id, agent_name, label, content)
		 SELECT ?, ?, ?, ?
		 WHERE NOT EXISTS (SELECT 1 FROM prompt_versions WHERE agent_name = ?)`,
		uuid.New().String(), agentName, label, content, agentName,
	)
	return err
}

// GetActivePromptVersions returns all non-disabled versions for an agent.
func (s *Store) GetActivePromptVersions(ctx context.Context, agentName string) ([]metrics.PromptVersion, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, agent_name, label, content, disabled, created_at
		 FROM prompt_versions WHERE agent_name = ? AND disabled = 0
		 ORDER BY created_at ASC`, agentName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPromptVersions(rows)
}

// ListPromptVersions returns all versions for an agent (including disabled).
func (s *Store) ListPromptVersions(ctx context.Context, agentName string) ([]metrics.PromptVersion, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, agent_name, label, content, disabled, created_at
		 FROM prompt_versions WHERE agent_name = ?
		 ORDER BY created_at ASC`, agentName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanPromptVersions(rows)
}

// AddPromptVersion inserts a new prompt version. It generates a UUID if pv.ID is empty.
func (s *Store) AddPromptVersion(ctx context.Context, pv metrics.PromptVersion) error {
	if pv.ID == "" {
		pv.ID = uuid.New().String()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO prompt_versions (id, agent_name, label, content, disabled) VALUES (?, ?, ?, ?, ?)`,
		pv.ID, pv.AgentName, pv.Label, pv.Content, boolToInt(pv.Disabled),
	)
	return err
}

// DisablePromptVersion marks a version as disabled so it is excluded from A/B selection.
func (s *Store) DisablePromptVersion(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE prompt_versions SET disabled = 1 WHERE id = ?`, id)
	return err
}

// ── Review runs ──────────────────────────────────────────────────────────────

func (s *Store) SaveReviewRun(ctx context.Context, r metrics.ReviewRun) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO review_runs (id, pr_number, repo_owner, repo_name, head_sha, github_review_id)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		r.ID, r.PRNumber, r.RepoOwner, r.RepoName, r.HeadSHA, r.GitHubReviewID,
	)
	return err
}

func (s *Store) HasReviewedAtSHA(ctx context.Context, repoOwner, repoName string, prNumber int, headSHA string) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM review_runs
			WHERE repo_owner = ? AND repo_name = ? AND pr_number = ? AND head_sha = ?
			UNION ALL
			SELECT 1 FROM pr_skips
			WHERE repo_owner = ? AND repo_name = ? AND pr_number = ? AND head_sha = ?
		)`,
		repoOwner, repoName, prNumber, headSHA,
		repoOwner, repoName, prNumber, headSHA,
	).Scan(&exists)
	return exists, err
}

// RecordSkip inserts a pr_skips record so the poller will not re-review
// this PR+SHA. Idempotent — safe to call multiple times for the same PR+SHA.
func (s *Store) RecordSkip(ctx context.Context, repoOwner, repoName string, prNumber int, headSHA string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO pr_skips (repo_owner, repo_name, pr_number, head_sha)
		 VALUES (?, ?, ?, ?)`,
		repoOwner, repoName, prNumber, headSHA,
	)
	return err
}

func (s *Store) SetGitHubReviewID(ctx context.Context, workflowID string, reviewID int64) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE review_runs SET github_review_id = ? WHERE id = ?`, reviewID, workflowID)
	return err
}

// ── Agent runs ───────────────────────────────────────────────────────────────

func (s *Store) SaveAgentRun(ctx context.Context, r metrics.AgentRun) error {
	if r.ID == "" {
		r.ID = uuid.New().String()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO agent_runs
		 (id, review_run_id, agent_name, status, model, input_tokens, output_tokens, latency_ms, findings_count, prompt_version_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.ReviewRunID, r.AgentName, r.Status, r.Model,
		r.InputTokens, r.OutputTokens, r.LatencyMS, r.FindingsCount,
		nullableString(r.PromptVersionID),
	)
	return err
}

// GetAgentRunID returns the UUID of the agent run for a given workflow+agent pair.
func (s *Store) GetAgentRunID(ctx context.Context, workflowID, agentName string) (string, bool, error) {
	var id string
	err := s.db.QueryRowContext(ctx,
		`SELECT id FROM agent_runs WHERE review_run_id = ? AND agent_name = ? LIMIT 1`,
		workflowID, agentName,
	).Scan(&id)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	return id, err == nil, err
}

// ── Findings ─────────────────────────────────────────────────────────────────

func (s *Store) SaveFindings(ctx context.Context, findings []metrics.FindingRecord) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit; not actionable on earlier failure
	stmt, err := tx.PrepareContext(ctx,
		`INSERT OR IGNORE INTO findings (id, agent_run_id, severity, title, file_path, line_number, github_comment_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, f := range findings {
		if f.ID == "" {
			f.ID = uuid.New().String()
		}
		if _, err := stmt.ExecContext(ctx, f.ID, f.AgentRunID, f.Severity, f.Title, f.FilePath, f.LineNumber, f.GitHubCommentID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) GetFindingsByReviewRun(ctx context.Context, workflowID string) ([]metrics.FindingRecord, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT f.id, f.agent_run_id, f.severity, f.title, f.file_path, f.line_number, f.github_comment_id, f.created_at
		 FROM findings f
		 JOIN agent_runs a ON a.id = f.agent_run_id
		 WHERE a.review_run_id = ?`, workflowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFindings(rows)
}

func (s *Store) GetFindingByCommentID(ctx context.Context, commentID int64) (metrics.FindingRecord, bool, error) {
	var f metrics.FindingRecord
	err := s.db.QueryRowContext(ctx,
		`SELECT id, agent_run_id, severity, title, file_path, line_number, github_comment_id, created_at
		 FROM findings WHERE github_comment_id = ?`, commentID,
	).Scan(&f.ID, &f.AgentRunID, &f.Severity, &f.Title, &f.FilePath, &f.LineNumber, &f.GitHubCommentID, &f.CreatedAt)
	if err == sql.ErrNoRows {
		return metrics.FindingRecord{}, false, nil
	}
	return f, err == nil, err
}

// ── Feedback ─────────────────────────────────────────────────────────────────

func (s *Store) SaveFeedback(ctx context.Context, f metrics.FeedbackEvent) error {
	if f.ID == "" {
		f.ID = uuid.New().String()
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO feedback_events (id, finding_id, verdict, source) VALUES (?, ?, ?, ?)`,
		f.ID, f.FindingID, f.Verdict, f.Source,
	)
	return err
}

// ── Metrics queries ───────────────────────────────────────────────────────────

func (s *Store) GetAgentMetrics(ctx context.Context, agentName string, since time.Time) (metrics.AgentMetrics, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			COUNT(DISTINCT ar.id)                                          AS review_count,
			COALESCE(SUM(ar.findings_count), 0)                           AS findings_total,
			COALESCE(SUM(CASE WHEN fe.verdict = 'fp' THEN 1 ELSE 0 END), 0) AS false_positives,
			COALESCE(SUM(CASE WHEN fe.verdict = 'tp' THEN 1 ELSE 0 END), 0) AS true_positives,
			COALESCE(AVG(ar.latency_ms), 0)                               AS avg_latency,
			COALESCE(AVG(ar.input_tokens), 0)                             AS avg_input,
			COALESCE(AVG(ar.output_tokens), 0)                            AS avg_output
		FROM agent_runs ar
		LEFT JOIN findings f ON f.agent_run_id = ar.id
		LEFT JOIN feedback_events fe ON fe.finding_id = f.id
		WHERE ar.agent_name = ? AND ar.created_at >= ?`,
		agentName, since.UTC().Format(time.RFC3339),
	)
	var m metrics.AgentMetrics
	m.AgentName = agentName
	if err := row.Scan(&m.ReviewCount, &m.FindingsTotal, &m.FalsePositives, &m.TruePositives,
		&m.AvgLatencyMS, &m.AvgInputTokens, &m.AvgOutputTokens); err != nil {
		return m, err
	}
	if m.FindingsTotal > 0 {
		m.FPRate = float64(m.FalsePositives) / float64(m.FindingsTotal)
	}
	return m, nil
}

func (s *Store) GetPromptVersionMetrics(ctx context.Context, promptVersionID string) (metrics.PromptVersionMetrics, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT
			pv.agent_name,
			pv.label,
			COUNT(DISTINCT ar.id)                                            AS review_count,
			COALESCE(SUM(ar.findings_count), 0)                             AS findings_total,
			COALESCE(SUM(CASE WHEN fe.verdict = 'fp' THEN 1 ELSE 0 END), 0) AS false_positives,
			COALESCE(SUM(CASE WHEN fe.verdict = 'tp' THEN 1 ELSE 0 END), 0) AS true_positives
		FROM prompt_versions pv
		LEFT JOIN agent_runs ar ON ar.prompt_version_id = pv.id
		LEFT JOIN findings f ON f.agent_run_id = ar.id
		LEFT JOIN feedback_events fe ON fe.finding_id = f.id
		WHERE pv.id = ?
		GROUP BY pv.id`, promptVersionID,
	)
	var m metrics.PromptVersionMetrics
	m.PromptVersionID = promptVersionID
	if err := row.Scan(&m.AgentName, &m.Label, &m.ReviewCount, &m.FindingsTotal,
		&m.FalsePositives, &m.TruePositives); err != nil {
		return m, err
	}
	if m.FindingsTotal > 0 {
		m.FPRate = float64(m.FalsePositives) / float64(m.FindingsTotal)
	}
	return m, nil
}

func (s *Store) ListAgentMetrics(ctx context.Context, since time.Time) ([]metrics.AgentMetrics, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT agent_name FROM agent_runs WHERE created_at >= ?`,
		since.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var result []metrics.AgentMetrics
	for _, n := range names {
		m, err := s.GetAgentMetrics(ctx, n, since)
		if err != nil {
			return nil, err
		}
		result = append(result, m)
	}
	return result, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func scanPromptVersions(rows *sql.Rows) ([]metrics.PromptVersion, error) {
	var out []metrics.PromptVersion
	for rows.Next() {
		var v metrics.PromptVersion
		var disabled int
		if err := rows.Scan(&v.ID, &v.AgentName, &v.Label, &v.Content, &disabled, &v.CreatedAt); err != nil {
			return nil, err
		}
		v.Disabled = disabled != 0
		out = append(out, v)
	}
	return out, rows.Err()
}

func scanFindings(rows *sql.Rows) ([]metrics.FindingRecord, error) {
	var out []metrics.FindingRecord
	for rows.Next() {
		var f metrics.FindingRecord
		if err := rows.Scan(&f.ID, &f.AgentRunID, &f.Severity, &f.Title, &f.FilePath, &f.LineNumber, &f.GitHubCommentID, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullableString(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}
