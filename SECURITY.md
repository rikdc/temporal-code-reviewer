# Threat Model and State Ownership

## Deployment assumptions

The service runs on a private workstation, home server, or trusted internal
network. It is not exposed as a public internet service. Nevertheless, every
external input is treated as untrusted.

## Trust boundaries

| Boundary | Trusted side | Untrusted side |
|---|---|---|
| GitHub webhook | Service logic | HTTP request body, headers, sender |
| GitHub API responses | Service logic | API payloads (may be stale or rate-limited) |
| LLM responses | Service logic | OpenRouter output (may be malformed, hallucinated, or adversarial) |
| Dashboard browser | Service logic | Browser requests, SSE clients |
| Temporal | Workflow/activity code | Workflow history replay, clock skew |
| SQLite | Application state | Disk corruption, concurrent writes |
| Filesystem secrets | Service startup | File permissions, symlink attacks |

## Threat catalogue

### GitHub payloads

- PR authors control every byte of their diffs.
- Webhook payloads can be forged unless cryptographically verified.
- `sender.login` is untrusted; it must not be used to determine PR ownership
  or auto-fix eligibility.
- `diff_url` in the payload is untrusted; diffs must be fetched through the
  authenticated GitHub API using repository identity and PR number.
- Delivery IDs (`X-GitHub-Delivery`) must be checked for deduplication.

### LLM output

- Responses may be malformed, truncated, or not valid JSON.
- Suggested fixes may reference files or lines that do not exist.
- Patches may be adversarially crafted to modify unrelated code.
- Token usage is unbounded without explicit limits.

### Temporal

- An activity may succeed externally but fail before Temporal records its
  completion (at-least-once execution).
- The process may restart at any point during workflow execution.
- Workflow history may replay activities; all activities must be idempotent.

### Network

- GitHub API calls may time out, be rate-lated, or partially succeed.
- OpenRouter calls may time out or return partial responses.
- The service may be accessed by other hosts on the private network.

### Process lifecycle

- Users may open the dashboard after a workflow has already started.
- The application may restart at any point, losing in-memory state.

## State ownership model

| Owner | Responsibilities |
|---|---|
| **Temporal** | Workflow execution state, retry state, timer management, schedule state. |
| **GitHub** | PR state, review comments, reactions, branches, commits, repository metadata. |
| **SQLite** | Local durable projection: review runs, agent runs, findings, feedback observations, prompt versions, dedup records, operational metrics. |
| **In-memory channels** | Live SSE notifications only. Never a source of truth. Must be reconstituted from SQLite on restart. |

## Feedback semantics (quarantined)

Raw observations are recorded but not interpreted as ground truth:

- Reaction type, actor, comment ID, and timestamp.
- Reply body, actor, target comment, and timestamp.
- Review comment resolution or deletion.
- Whether the reviewed code changed near the finding.
- PR close or merge state.

The system does **not** currently assume:

- Every reply is a true positive.
- Every deleted comment is a false positive.
- Every resolved comment indicates agreement.
- Absence of feedback indicates correctness.
- The first reaction permanently determines the verdict.

## Design principles

1. **Refuse unsafe operations** over producing plausible but unverifiable results.
2. **Immutable commit SHAs** for all file reads and review operations.
3. **Every external mutation** has an idempotency and retry story.
4. **Auto-fix disabled by default**; requires explicit opt-in for both repository
   and PR author.
5. **Partial reviews never claim success**; truncation produces `incomplete`
   status.
6. **Webhook verification required** when webhook is enabled.
7. **Admin authentication required** for all data-bearing endpoints.
8. **Loopback binding by default** for all services.
