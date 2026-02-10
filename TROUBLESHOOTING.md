# Temporal Code Reviewer Troubleshooting Guide

## Issue: UI Updates Too Fast / No LLM Calls

If the dashboard updates immediately without making OpenRouter API calls:

### 1. Check OpenRouter API Key

```bash
./scripts/check-setup.sh
```

The API key MUST be set as an environment variable:

```bash
export OPENROUTER_API_KEY='your-key-here'
```

**Common Mistakes:**
- ❌ Setting in `.env` file (not automatically loaded)
- ❌ Setting in `config.yaml` only (environment variable overrides)
- ❌ Not exporting (just setting in shell)

**Correct Method:**
```bash
# Option 1: Export directly
export OPENROUTER_API_KEY='sk-or-...'

# Option 2: Source from .env (after creating it)
cp .env.example .env
# Edit .env with your key
set -a && source .env && set +a

# Option 3: Set in shell profile
echo 'export OPENROUTER_API_KEY="sk-or-..."' >> ~/.zshrc
source ~/.zshrc
```

### 2. Rebuild After Changes

```bash
# Clean build to ensure no cached old code
rm temporal-code-reviewer
go build

# Verify build date
ls -lh temporal-code-reviewer
```

### 3. Check Application Logs

When Temporal Code Reviewer starts, you should see:

```
Configuration loaded successfully
  openrouter_url: https://openrouter.ai/api/v1
  api_key_set: true      <- MUST be true!
```

If `api_key_set: false`, the environment variable isn't set correctly.

### 4. Monitor LLM Calls

Watch the logs when triggering a review:

```bash
# In one terminal - start Temporal Code Reviewer
./temporal-code-reviewer

# In another terminal - watch for LLM calls
./scripts/test-with-real-pr.sh
```

You should see these log messages:
- `"Sending LLM request"` - When calling OpenRouter
- `"LLM request completed"` - With token counts and latency

If you don't see these, the agents aren't making LLM calls.

### 5. Verify Agent Configuration

Check `config.yaml`:

```yaml
agents:
  security:
    model: "anthropic/claude-3.5-sonnet"
    prompt_file: "prompts/security.md"
```

Verify all prompt files exist:
```bash
ls -la prompts/*.md
```

## Issue: Diff Not Found

If you see `"fetch diff: status 404"`:

### For GitHub Repositories

Use a **public** repository with an **actual PR**:

```bash
# Good: Real public PR
https://github.com/owner/repo/pull/123.diff

# Bad: Private repo (needs auth)
https://github.com/private/repo/pull/123.diff

# Bad: Non-existent PR
https://github.com/owner/repo/pull/999999.diff
```

### For Local Repositories

The diff fetcher requires HTTP/HTTPS URLs. To test with a local repo:

1. Generate a diff file:
```bash
cd /path/to/your/repo
git diff HEAD~1 > /tmp/test.diff
```

2. Serve it locally:
```bash
cd /tmp
python3 -m http.server 8090
```

3. Update trigger script to use:
```json
"diff_url": "http://localhost:8090/test.diff"
```

## Issue: Application Won't Start

### Check Temporal Server

```bash
# Start Temporal dev server
temporal server start-dev

# Verify it's running
curl http://localhost:7233
```

### Check Port Conflicts

Temporal Code Reviewer uses these ports:
- `8081` - Dashboard
- `8082` - Webhook API
- `7233` - Temporal (external)

If ports are in use:
```bash
# Find what's using a port
lsof -i :8081
lsof -i :8082

# Kill process
kill -9 <PID>
```

## Issue: Workflow Fails Immediately

Check Temporal UI for error details:
```
http://localhost:8080
```

Common errors:

### "activity not registered"

Rebuild the application:
```bash
go build
```

### "prompt file not found"

Verify prompts directory:
```bash
ls prompts/
```

Should contain:
- security.md
- style.md
- logic.md
- documentation.md

### "config validation failed"

Check `config.yaml` syntax:
```bash
yamllint config.yaml
```

## Quick Diagnosis

Run all checks at once:
```bash
./scripts/check-setup.sh
```

This validates:
1. ✓ OPENROUTER_API_KEY set
2. ✓ config.yaml valid
3. ✓ Prompt files present
4. ✓ Binary built
5. ✓ Temporal running
6. ✓ Temporal Code Reviewer service running

## Still Having Issues?

1. Check Temporal Code Reviewer logs for errors
2. Check Temporal UI for workflow failures
3. Verify OpenRouter API key is valid: https://openrouter.ai/keys
4. Try with a known-good public PR:
   ```bash
   ./scripts/test-with-real-pr.sh
   ```

## Expected Behavior

When working correctly:

1. **Startup logs:**
   ```
   Configuration loaded successfully
   Initializing OpenRouter LLM client
   Temporal Code Reviewer service started
   ```

2. **Workflow execution (visible in logs):**
   ```
   Fetching PR diff
   Diff fetched successfully  size=15432
   Sending LLM request  agent=Security model=anthropic/claude-3.5-sonnet
   LLM request completed  latency=3.2s input_tokens=1234 output_tokens=567
   Sending LLM request  agent=Style model=anthropic/claude-3.5-haiku
   ...
   ```

3. **Dashboard shows:**
   - All 4 agents progress from 0% → 100%
   - Detailed findings from LLM responses
   - Model names and token counts

4. **Timing:**
   - Total workflow: 30-60 seconds
   - Each agent: 5-15 seconds (depending on model and diff size)
