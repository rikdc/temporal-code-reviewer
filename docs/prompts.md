# Prompt Authoring Guide

This guide explains how the prompt system works and how to write or update prompts for the review agents.

---

## How the prompt system works

There are two tiers:

1. **Disk files** (`prompts/*.md`) — the source of truth checked into the repository. On startup, each agent's disk file is seeded into the SQLite database as version `v1` if no database version exists yet for that agent.

2. **Database versions** (`prompt_versions` table in `~/.config/prism/metrics.db`) — active versions selected at runtime. If an agent has one or more non-disabled database versions, the registry picks one at random (equal weight) instead of reading the disk file. This enables A/B testing between prompt variants without redeploying.

The selection logic (`metrics/prompt_registry.go`):

```
DB has active versions? → pick one at random
No DB versions?         → fall back to disk file
```

---

## The five agents

| Agent | Disk file | DB name | Role |
|---|---|---|---|
| Security | `prompts/security.md` | `Security` | OWASP vulnerabilities, secrets, injection |
| Style | `prompts/style.md` | `Style` | Go naming, formatting, godoc, code quality |
| Logic | `prompts/logic.md` | `Logic` | Bugs, nil dereferences, race conditions, edge cases |
| Documentation | `prompts/documentation.md` | `Documentation` | Missing or incorrect comments and docs |
| Triage | `prompts/triage.md` | `Triage` | Classifies findings as auto-fixable or human-required |

Security, Style, Logic, and Documentation are **review agents** — they receive the PR diff and return structured findings. Triage is a **classifier** — it receives the combined findings from all four review agents and decides which can be auto-fixed.

---

## Updating a prompt on disk

Edit the file under `prompts/`. The change takes effect on next startup if no database version exists for that agent, or immediately if you clear the DB entry for that agent:

```bash
sqlite3 ~/.config/prism/metrics.db \
  "UPDATE prompt_versions SET disabled = 1 WHERE agent_name = 'Security';"
```

After disabling all DB versions for an agent the registry falls back to the disk file on the next review.

---

## Adding a new prompt version (A/B testing)

Use the metrics repository's `AddPromptVersion` method. There is no HTTP endpoint for this today, so insert directly into the database:

```bash
sqlite3 ~/.config/prism/metrics.db <<'SQL'
INSERT INTO prompt_versions (id, agent_name, label, content, disabled)
VALUES (
  lower(hex(randomblob(16))),  -- UUID
  'Security',                   -- must match the DB name in the table above
  'stricter-v2',                -- human-readable label for metrics dashboards
  '<full prompt content here>',
  0
);
SQL
```

Multiple active versions for the same agent are selected randomly across reviews, so traffic splits evenly. Track performance per version via `GET /api/metrics` — each `agent_run` row records the `prompt_version_id` used.

To disable a version:

```bash
sqlite3 ~/.config/prism/metrics.db \
  "UPDATE prompt_versions SET disabled = 1 WHERE id = '<uuid>';"
```

---

## JSON output contract (review agents)

**All four review agents must return only raw JSON — no markdown fences, no preamble, no trailing text.** The parser will fail if any non-JSON text appears. The required schema:

```json
{
  "status": "passed" | "warning" | "failed",
  "findings": [
    {
      "severity": "critical" | "high" | "medium" | "low",
      "title": "One sentence, no trailing period",
      "description": "Why this is a problem and what can go wrong. Do not summarise what the diff does.",
      "file": "relative/path/to/file.go",
      "line": 42,
      "suggested_fix": "Raw code only. No backtick fences — the UI adds them automatically."
    }
  ],
  "summary": "Overall one-paragraph assessment"
}
```

### Status values

| Value | Meaning |
|---|---|
| `passed` | No issues found |
| `warning` | Minor issues the author should consider |
| `failed` | Issues that should block merge |

### Severity levels

| Value | Guidance |
|---|---|
| `critical` | Immediately exploitable or will definitely crash |
| `high` | Concrete risk with a clear exploit or failure path |
| `medium` | Real problem with a required fix and a clear reason it matters |
| `low` | Nice-to-have; no concrete consequence stated |

### `file` and `line`

Populate from the diff headers (`+++ b/path/to/file.go`). `line` is the line number in the **new** version of the file. Both are best-effort — the system degrades gracefully to body-only findings if a line number falls outside the diff hunk.

### `suggested_fix`

Raw code only. No markdown backtick fences. The posting layer wraps it in a fenced Go block automatically. Show only the changed lines or a minimal complete snippet — not the entire function.

---

## JSON output contract (triage agent)

The triage agent receives a JSON array of findings and must return:

```json
{
  "decisions": [
    {
      "finding_title": "Exact title of the finding",
      "auto_fixable": true | false,
      "reason": "One sentence explaining the classification",
      "fix_instructions": "Precise step-by-step instructions for an AI to apply the fix. Empty string if human-required."
    }
  ]
}
```

One decision per input finding, matched by exact `finding_title`. When `auto_fixable` is `false`, `fix_instructions` must be an empty string.

---

## Universal prompt rules

These apply to every agent:

1. **JSON only.** The first character of the response must be `{`. Any text before or after causes a parse failure and the finding is recorded as a `Raw LLM Response` placeholder.

2. **Do not summarise the diff.** Findings must describe what can go wrong, not what the code does. Summaries of changes ("this renames X", "this adds Y") are explicitly filtered out and add noise.

3. **Only include actionable findings.** If you cannot state a specific required change and a concrete consequence, omit the finding. The triage and false-positive tracking pipeline penalises high false-positive rates.

4. **Return valid JSON when there are no findings.** An empty `findings` array with an appropriate `summary` is correct output.

5. **`suggested_fix` is raw code.** No backtick fences, no markdown. The UI renders it inside a fenced block.

---

## Writing a prompt for a new agent

If you are adding a new review agent to the codebase (which also requires code changes — see below), the prompt file must:

1. Open with the JSON-only mandate:
   ```
   **CRITICAL: You MUST respond ONLY with valid JSON. Do not include any text before or after the JSON.**
   ```

2. Define the agent's role and what it should focus on.

3. Include an explicit **Do NOT Report** section to suppress noise. Be specific about what kinds of observations the agent should omit.

4. Include the full JSON schema with inline guidance on each field.

5. Include a concrete example with two or three realistic findings.

6. Close with the importance reminders (always return valid JSON, omit uncertain findings).

Use any of the existing files in `prompts/` as a template.

---

## Adding a new agent to the codebase

Prompts alone are not enough — a new review agent requires code changes:

1. **Config** (`config/config.go`): Add a field to `AgentConfigs` and update `config.yaml` / `config.bedrock.yaml` with model, temperature, max tokens, and `prompt_file`.

2. **Activity** (`activities/review_agent.go`): Add a constructor (e.g. `NewPerformanceAgent`) following the pattern of the existing four.

3. **Activity name** (`activities/names.go`): Add a constant (e.g. `ActivityPerformance = "PerformanceAgent.Execute"`).

4. **Workflow** (`workflows/pr_review.go`): Add the agent to the parallel fan-out in Phase 1 and collect its result.

5. **Registration** (`main.go`): Register the activity with the worker.

6. **Prompt file** (`prompts/performance.md`): Write the prompt following the rules above.

The new agent's prompt will be seeded into the database as `v1` on first startup.
