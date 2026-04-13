---
name: prompt-improver
description: Analyze the code review feedback database and propose data-driven improvements to agent prompts. Use this skill when the user wants to improve review quality, reduce false positives, tune a specific agent (Security, Style, Logic, Documentation, Triage), or add a new prompt variant for A/B testing. Also trigger when the user mentions "prompt improvement", "false positives are too high", "reviews are noisy", "tune the reviewer", or "update the prompts".
---

# Prompt Improver

This skill analyzes feedback data from `~/.config/prism/metrics.db` to identify which review agent prompts are performing poorly and proposes specific, well-reasoned improvements. It then generates SQL ready to deploy the new prompt version into the A/B testing system.

## Context

This project runs five LLM-powered review agents (Security, Style, Logic, Documentation, Triage) against PR diffs. Each agent has a versioned prompt stored in `prompts/<agent>.md` and optionally in the SQLite database for A/B testing. User feedback is collected implicitly (deleted GitHub comments = false positive) and explicitly (POST /api/feedback). The goal is to minimize false positive rate while keeping true positive signal high.

Read `docs/prompts.md` for the full JSON output contract each agent must follow, and `references/db-schema.md` for the database schema.

---

## Analysis Workflow

### Step 1: Establish Scope

Ask the user (or infer from context) which agent to focus on:
- **Single agent**: e.g. "Style is too noisy" → analyze only Style
- **All agents**: run the full analysis and rank by worst performer
- **A specific prompt version**: compare versions head-to-head

Default time window is the last 30 days. Ask if they want a different window (7 days for recent changes, 90 days for a longer trend).

### Step 2: Query the Database

Run the analysis queries below using `sqlite3 ~/.config/prism/metrics.db`. Present the results as a summary table before proceeding to recommendations.

#### 2a. Agent-level performance summary
```sql
SELECT
    ar.agent_name,
    COUNT(DISTINCT ar.id)                                          AS total_runs,
    COUNT(DISTINCT f.id)                                          AS total_findings,
    COUNT(DISTINCT CASE WHEN fe.verdict = 'fp' THEN fe.id END)   AS false_positives,
    COUNT(DISTINCT CASE WHEN fe.verdict = 'tp' THEN fe.id END)   AS true_positives,
    ROUND(
        100.0 * COUNT(DISTINCT CASE WHEN fe.verdict = 'fp' THEN fe.id END) /
        NULLIF(COUNT(DISTINCT f.id), 0), 1
    )                                                             AS fp_rate_pct,
    ROUND(AVG(DISTINCT ar.latency_ms) / 1000.0, 1)               AS avg_latency_sec
FROM agent_runs ar
LEFT JOIN findings f ON f.agent_run_id = ar.id
LEFT JOIN feedback_events fe ON fe.finding_id = f.id
WHERE ar.created_at >= datetime('now', '-30 days')
GROUP BY ar.agent_name
ORDER BY fp_rate_pct DESC;
```

#### 2b. Prompt version comparison (for agent under focus)
```sql
SELECT
    pv.label,
    pv.id,
    COUNT(DISTINCT ar.id)                                          AS runs,
    COUNT(DISTINCT f.id)                                          AS total_findings,
    COUNT(DISTINCT CASE WHEN fe.verdict = 'fp' THEN fe.id END)   AS false_positives,
    ROUND(
        100.0 * COUNT(DISTINCT CASE WHEN fe.verdict = 'fp' THEN fe.id END) /
        NULLIF(COUNT(DISTINCT f.id), 0), 1
    )                                                             AS fp_rate_pct
FROM prompt_versions pv
JOIN agent_runs ar ON ar.prompt_version_id = pv.id
LEFT JOIN findings f ON f.agent_run_id = ar.id
LEFT JOIN feedback_events fe ON fe.finding_id = f.id
WHERE pv.agent_name = '<AGENT_NAME>'          -- replace with target agent
  AND ar.created_at >= datetime('now', '-30 days')
GROUP BY pv.id
ORDER BY fp_rate_pct DESC;
```

#### 2c. Top false-positive finding patterns
```sql
SELECT
    f.severity,
    f.title,
    COUNT(*)                                                       AS total_times_raised,
    COUNT(CASE WHEN fe.verdict = 'fp' THEN 1 END)                AS fp_count,
    ROUND(
        100.0 * COUNT(CASE WHEN fe.verdict = 'fp' THEN 1 END) /
        NULLIF(COUNT(*), 0), 1
    )                                                             AS fp_rate_pct
FROM findings f
JOIN agent_runs ar ON ar.id = f.agent_run_id
LEFT JOIN feedback_events fe ON fe.finding_id = f.id
WHERE ar.agent_name = '<AGENT_NAME>'          -- replace with target agent
  AND ar.created_at >= datetime('now', '-30 days')
GROUP BY f.title
HAVING total_times_raised >= 2
ORDER BY fp_rate_pct DESC, total_times_raised DESC
LIMIT 20;
```

#### 2d. Feedback source breakdown (to understand data quality)
```sql
SELECT
    ar.agent_name,
    fe.source,
    fe.verdict,
    COUNT(*) AS count
FROM feedback_events fe
JOIN findings f ON f.id = fe.finding_id
JOIN agent_runs ar ON ar.id = f.agent_run_id
WHERE ar.created_at >= datetime('now', '-30 days')
GROUP BY ar.agent_name, fe.source, fe.verdict
ORDER BY ar.agent_name, count DESC;
```

If the database has very little feedback data (< 20 findings with feedback), note this explicitly — proposals will be more speculative and should be framed as experiments rather than data-driven fixes.

### Step 3: Read the Current Prompt

Use the **Read tool** to read the prompt file — do not use Bash for this, since file reads are always permitted regardless of environment permissions:

```
Read: /path/to/repo/prompts/<agent_lowercase>.md
```

Also check for active database versions (via sqlite3 if available, otherwise skip):
```sql
SELECT id, label, substr(content, 1, 200) AS preview, disabled
FROM prompt_versions
WHERE agent_name = '<AGENT_NAME>'
ORDER BY created_at;
```

Understand what the current prompt is asking the agent to do. Pay attention to:
- What it explicitly says to report
- What it says to **not** report
- The examples it gives (examples heavily influence LLM behavior)
- The severity guidance

### Step 4: Diagnose the Root Cause

Before proposing a fix, form a hypothesis about *why* false positives are occurring. Common patterns:

| Pattern | Likely Cause | Typical Fix |
|---------|-------------|-------------|
| High FP rate on a specific title (e.g. "Missing godoc") | Overly broad trigger condition | Add a more specific "Do NOT report if..." clause |
| High FP rate on `low` severity findings | Low-bar threshold; noise | Raise the bar for what earns a `low` finding |
| High FP rate across many different titles | System prompt is too permissive | Tighten the "only report actionable findings" section |
| High FP rate on certain file types or patterns | Agent doesn't understand domain context | Add explicit exclusion examples |
| TP count near zero (no positive feedback) | Either findings are all FPs, or users aren't giving positive feedback | Interpret cautiously; check manually if possible |

Document your diagnosis clearly — it becomes the justification for the change.

### Step 5: Draft the Improved Prompt

Modify the current prompt to address the diagnosed issue. Follow these principles:

**Be surgical.** Change only what needs changing. A prompt that worked well in some areas shouldn't be gutted.

**Add specificity, not vagueness.** If the agent is over-reporting "missing godoc on private helpers", add a precise rule:
> Do NOT report missing godoc on unexported (lowercase) identifiers — Go convention does not require this.

Rather than a vague instruction to "be more careful."

**Explain the why.** Prompts work better when the reasoning is visible. Instead of:
> Do not flag error wrapping style

Write:
> Do not flag error wrapping style (e.g. `%w` vs `%v`) — these are stylistic preferences without a single correct answer, and flagging them generates noise for the reviewer.

**Preserve the output contract.** Every review agent must still return valid JSON matching the schema in `docs/prompts.md`. Do not change the JSON structure, field names, or status values.

**For the Triage agent specifically:** Changes to triage affect *all* agents' outputs, since triage classifies everyone's findings. Be conservative — a triage change that incorrectly marks something as auto-fixable can cause automated edits to code.

### Step 6: Present the Proposal

Show the user:

1. **Data summary** — the metrics that motivated the change (1–2 paragraphs or a table)
2. **Diagnosis** — what you believe is causing the false positives
3. **Proposed change** — a clear diff of what changed in the prompt (old vs new for the affected section)
4. **Rationale** — why this specific change addresses the diagnosis
5. **Risk assessment** — could this change suppress true positives? What would you monitor?

Ask for the user's feedback before generating the SQL — they may want to refine the wording.

### Step 7: Generate the SQL

Once the user approves the prompt text, generate a ready-to-run SQL INSERT:

```sql
-- Add new prompt version for <AGENT_NAME>
-- Label convention: describe the change (e.g. "reduce-godoc-noise-v2")
sqlite3 ~/.config/prism/metrics.db <<'SQL'
INSERT INTO prompt_versions (id, agent_name, label, content, disabled)
VALUES (
  lower(hex(randomblob(16))),
  '<AGENT_NAME>',
  '<descriptive-label>',
  '<FULL PROMPT CONTENT — no single quotes, escape with '' if needed>',
  0
);
SQL
```

Label naming convention: `<what-changed>-v<N>`. Examples:
- `reduce-godoc-noise-v2`
- `stricter-security-injection-v3`
- `triage-conservative-autofix-v2`

Also provide the SQL to disable the current disk-seed version if the user wants the new version to be the only active one:
```sql
sqlite3 ~/.config/prism/metrics.db \
  "UPDATE prompt_versions SET disabled = 1 WHERE agent_name = '<AGENT_NAME>' AND label = 'v1';"
```

Remind the user that multiple active versions for the same agent are selected randomly (A/B test), and that metrics can be compared via `GET /api/metrics` using the `prompt_version_id` field in agent runs.

---

## Handling Multiple Agents

When analyzing all agents at once, rank them by FP rate and focus the improvement proposal on the worst performer. Briefly note the others so the user has the full picture. Don't try to propose improvements for all five agents in one response — it's overwhelming and the changes are likely to conflict.

If the user wants to improve multiple agents, handle them one at a time in priority order.

---

## Handling New Agent Categories

If the user mentions adding a new agent type (e.g. "Performance agent", "Accessibility agent"):

1. Read `docs/prompts.md` — the "Writing a prompt for a new agent" and "Adding a new agent to the codebase" sections describe what's needed both for the prompt file and for code changes.
2. Note that new agent categories require code changes in addition to a new prompt file — list the files that need to change.
3. Draft the new prompt following the structure in `docs/prompts.md`, which requires: JSON-only mandate, role definition, explicit "Do NOT report" section, full JSON schema, concrete examples, and closing reminders.
4. The new prompt doesn't go into the database until the code changes are deployed and the system starts up (it gets seeded as `v1` automatically).

---

## Output Quality Checklist

Before presenting a proposal, verify:
- [ ] The proposed prompt still begins with the JSON-only mandate (`**CRITICAL: You MUST respond ONLY with valid JSON.**`)
- [ ] The JSON output schema is unchanged
- [ ] The severity definitions are unchanged or explicitly updated with justification
- [ ] The "Do NOT report" section is specific enough to be actionable (not just "be careful")
- [ ] The change is targeted — other well-performing sections of the prompt are untouched
- [ ] The SQL label is descriptive and follows the naming convention
- [ ] The user has confirmed the prompt text before the SQL is generated
