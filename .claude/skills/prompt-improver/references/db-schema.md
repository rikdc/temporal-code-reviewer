# Metrics Database Schema Reference

**Database path:** `~/.config/prism/metrics.db` (SQLite)

## Tables

### `prompt_versions`
| Column | Type | Notes |
|--------|------|-------|
| id | TEXT PK | UUID |
| agent_name | TEXT | Security, Style, Logic, Documentation, Triage |
| label | TEXT | Human-readable version label (e.g. "v1", "stricter-v2") |
| content | TEXT | Full prompt text |
| disabled | INTEGER | 0 = active, 1 = disabled |
| created_at | DATETIME | |

### `review_runs`
| Column | Type | Notes |
|--------|------|-------|
| id | TEXT PK | Temporal workflow ID |
| pr_number | INTEGER | |
| repo_owner | TEXT | |
| repo_name | TEXT | |
| head_sha | TEXT | Commit SHA |
| github_review_id | INTEGER | 0 until draft review is posted |
| created_at | DATETIME | |

### `agent_runs`
| Column | Type | Notes |
|--------|------|-------|
| id | TEXT PK | UUID |
| review_run_id | TEXT | FK → review_runs |
| agent_name | TEXT | Security, Style, Logic, Documentation, Triage |
| status | TEXT | passed, warning, failed |
| model | TEXT | LLM model name |
| input_tokens | INTEGER | |
| output_tokens | INTEGER | |
| latency_ms | INTEGER | |
| findings_count | INTEGER | |
| prompt_version_id | TEXT | NULL if disk fallback used |
| created_at | DATETIME | |

### `findings`
| Column | Type | Notes |
|--------|------|-------|
| id | TEXT PK | UUID |
| agent_run_id | TEXT | FK → agent_runs |
| severity | TEXT | critical, high, medium, low |
| title | TEXT | Brief finding title |
| file_path | TEXT | Relative path (empty for body-only findings) |
| line_number | INTEGER | 0 if no specific line |
| github_comment_id | INTEGER | 0 for body-only; GitHub comment ID for inline |
| created_at | DATETIME | |

### `feedback_events`
| Column | Type | Notes |
|--------|------|-------|
| id | TEXT PK | UUID |
| finding_id | TEXT | FK → findings |
| verdict | TEXT | tp (true positive), fp (false positive), ignored |
| source | TEXT | manual (API call), github_deleted (comment deleted) |
| created_at | DATETIME | |

## Useful Indexes

- `idx_prompt_versions_agent` on `prompt_versions(agent_name, disabled)` — A/B selection
- `idx_findings_agent_run` on `findings(agent_run_id)` — Findings per agent
- `idx_feedback_finding` on `feedback_events(finding_id)` — Feedback per finding

## Key Joins

```
findings → agent_runs → review_runs (lineage)
findings → feedback_events (quality signal)
agent_runs → prompt_versions (which prompt produced this run)
```

## Feedback Verdicts

| Verdict | Meaning | How collected |
|---------|---------|---------------|
| `fp` | False positive — finding was wrong or irrelevant | User deleted comment (github_deleted) or manual API call |
| `tp` | True positive — finding was valid and actioned | Manual API call |
| `ignored` | Reviewed but no action taken | Manual API call |
