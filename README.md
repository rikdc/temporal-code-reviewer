# Temporal Code Reviewer

A durable, parallel code review system coordinated by Temporal workflows. Four specialized review agents (Security, Style, Logic, Documentation) analyze PR diffs concurrently, with optional triage and auto-fix capabilities.

## Architecture

```
GitHub PR (webhook or poll) → Temporal Workflow
  Phase 0: Fetch diff (with coverage tracking)
  Phase 1: [Security, Style, Logic, Documentation] agents run in parallel
  Phase 2: Synthesis agent aggregates results
  Phase 3: Post draft GitHub review
  Phase 4: Triage agent classifies findings
  Phase 5-7: (optional) Auto-fix generation and PR creation
```

Each phase is a Temporal activity with retries and heartbeats. The workflow is durable — it survives process restarts and provides exactly-once execution guarantees.

## Operating environment

- **Runtime**: Go 1.26+, Temporal server, SQLite, GitHub API
- **Deployment**: Docker Compose (recommended) or bare metal
- **Network**: Private network only. Services bind to `127.0.0.1` by default.
- **Storage**: SQLite for local durable state. No PostgreSQL required for the application itself (only for Temporal backend).

## Trust boundaries

- GitHub webhook payloads are untrusted (see `SECURITY.md`).
- LLM output is untrusted and may be malformed or adversarial.
- The webhook verifies HMAC signatures before processing.
- All admin endpoints require bearer token authentication.
- Auto-fix is disabled by default and requires explicit opt-in.
- See `SECURITY.md` for the full threat model.

## Quick start

### 1. Configure secrets

```bash
cp .env.example .env
# Edit .env with your keys:
#   OPENROUTER_API_KEY=sk-or-...
#   GITHUB_TOKEN=ghp_...
#   ADMIN_API_TOKEN=your-admin-token
#   WEBHOOK_SECRET=your-webhook-secret
```

Or use file-backed secrets (recommended for production):

```yaml
# config.yaml
openrouter:
  api_key_file: /path/to/openrouter-key

webhook:
  enabled: true
  secret_file: /path/to/webhook-secret

admin:
  token_file: /path/to/admin-token
```

### 2. Start with Docker Compose

```bash
docker compose up -d
```

This starts:
- PostgreSQL (Temporal backend)
- Temporal server
- Temporal UI (http://localhost:8080)
- Temporal namespace initialization
- The review service (http://localhost:8082)

### 3. Start without Docker

Requires a running Temporal server at `localhost:7233`:

```bash
# Create namespace (if not using Docker Compose init)
temporal operator namespace create code-reviewer --rd 72h
temporal operator search-attribute create --namespace code-reviewer --name Repository --type Text
temporal operator search-attribute create --namespace code-reviewer --name PRAuthor --type Text

# Run the service
go run .
```

## Configuration

See `config.yaml` for all options. Key settings:

| Setting | Default | Description |
|---|---|---|
| `server.bind_address` | `127.0.0.1:8082` | API server bind address |
| `server.dashboard_address` | `127.0.0.1:8081` | Dashboard bind address |
| `webhook.enabled` | `false` | Enable GitHub webhook receiver |
| `webhook.secret` | (required when enabled) | HMAC-SHA256 webhook secret |
| `webhook.allowed_repos` | `[]` | Repository allowlist (`owner/repo`) |
| `admin.token` | (empty) | Bearer token for admin API |
| `poller.enabled` | `false` | Enable scheduled PR polling |
| `poller.interval_seconds` | `7200` | Polling interval (2 hours) |
| `auto_fix_users` | `[]` | GitHub logins eligible for auto-fix |

### Secrets

Secrets can be provided via:
1. **File-backed** (recommended): Set `secret_file`, `api_key_file`, or `token_file` in config.yaml
2. **Environment variables**: `OPENROUTER_API_KEY`, `GITHUB_TOKEN`, `ADMIN_API_TOKEN`, `WEBHOOK_SECRET`
3. **Config YAML**: `openrouter.api_key`, `webhook.secret`, `admin.token`

File-backed secrets are read at startup. Environment variables override YAML values.

## Webhook setup

1. In your GitHub repository, go to Settings → Webhooks → Add webhook.
2. Set **Payload URL** to `http://your-server:8082/webhook/pr`.
3. Set **Content type** to `application/json`.
4. Set **Secret** to your webhook secret.
5. Select **Pull requests** events.
6. Ensure **Active** is checked.

The webhook:
- Verifies `X-Hub-Signature-256` against the exact request body.
- Validates the event type is `pull_request`.
- Accepts only `opened`, `synchronize`, and `reopened` actions (configurable).
- Deduplicates deliveries by `X-GitHub-Delivery`.
- Enforces the repository allowlist when configured.
- Rejects oversized payloads (>2MB by default).

## Admin API

All admin endpoints require `Authorization: Bearer <token>` header when `admin.token` is configured.

| Endpoint | Method | Description |
|---|---|---|
| `/health` | GET | Health check (unauthenticated) |
| `/api/reviews` | GET | List all review records |
| `/api/reviews/stream` | GET | SSE stream of new reviews |
| `/api/reviews/submit` | POST | Submit a pending review |
| `/api/reviews/skip` | POST | Record a PR skip |
| `/api/reviews/delete` | DELETE | Clear dedup records |
| `/api/reviews/force` | POST | Force re-review |
| `/api/feedback` | POST | Record manual feedback |
| `/api/metrics` | GET | Agent metrics |

## Review coverage

When a diff exceeds 50,000 characters or 1,000 lines, it is truncated. The review result is marked `incomplete` and:
- The GitHub review body discloses the incomplete coverage.
- Auto-fix is disabled for that run.
- Metrics do not count it as a successful complete review.

## Auto-fix

Auto-fix is **disabled by default**. To enable:

1. Set `auto_fix_users` in config.yaml to the GitHub logins that should receive fix PRs.
2. The PR author must be in the allowlist (checked against GitHub API, not webhook sender).
3. The fix applies only to files in the reviewed diff that were covered by the review.
4. Patches are validated exactly before any GitHub branch is published.
5. Generated fix PRs require human review and are never merged automatically.

### Fork PRs

Auto-fix is not supported for fork-originated PRs. The system detects fork PRs and skips auto-fix with a clear reason.

## Feedback collection

Feedback monitoring is **disabled by default** (feedback poller only starts when a GitHub review ID exists). The poller records raw observations every 2 hours for up to 7 days:

- Comment deletions
- Reactions (+1, -1, heart, hooray, rocket, confused)
- Replies to review comments

These are stored as raw observations. The system does **not** interpret them as ground truth labels. See `SECURITY.md` for the quarantined feedback semantics.

## Dashboard

The dashboard at `http://localhost:8081/dashboard?workflowId=<id>` shows real-time agent progress via SSE. State is persisted to SQLite and survives application restarts.

## Testing

```bash
go test ./...
go test -race ./...
go vet ./...
```

## Backup and removal

- **SQLite database**: `~/.config/temporal-reviewer/metrics.db`
- **Temporal state**: Managed by Temporal server (PostgreSQL). To clear, delete the `code-reviewer` namespace.
- **Docker volumes**: `docker compose down -v` removes all data.

## Known limitations

- Diff truncation at 50K chars/1K lines means large PRs get incomplete reviews.
- Auto-fix does not support fork PRs.
- Feedback interpretation is quarantined — no automated quality labels.
- The feedback poller polls every 2 hours (configurable). Earlier versions polled every 5 minutes.
- Dashboard SSE is best-effort; the client loads a durable snapshot from SQLite on connect.

## License

MIT — see [LICENSE](LICENSE).
