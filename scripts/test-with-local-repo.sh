#!/bin/bash

# Test Lyon with a local Git repository

echo "🦁 Lyon PR Review - Local Repository Test"
echo "=========================================="
echo ""

# Check arguments
if [ -z "$1" ]; then
    echo "Usage: $0 <path-to-git-repo> [commit-range]"
    echo ""
    echo "Examples:"
    echo "  $0 /path/to/repo              # Compare HEAD with HEAD~1"
    echo "  $0 /path/to/repo HEAD~5       # Compare HEAD with HEAD~5"
    echo "  $0 /path/to/repo main..branch # Compare branch with main"
    echo ""
    exit 1
fi

REPO_PATH="$1"
COMMIT_RANGE="${2:-HEAD~1}"

# Verify repository exists
if [ ! -d "$REPO_PATH" ]; then
    echo "❌ Error: Directory not found: $REPO_PATH"
    exit 1
fi

# Verify it's a git repository
if [ ! -d "$REPO_PATH/.git" ]; then
    echo "❌ Error: Not a git repository: $REPO_PATH"
    exit 1
fi

echo "Repository: $REPO_PATH"
cd "$REPO_PATH" || exit 1

# Get repository name
REPO_NAME=$(basename "$REPO_PATH")
echo "Name: $REPO_NAME"
echo ""

# Generate diff
echo "Generating diff for: $COMMIT_RANGE..HEAD"
DIFF_FILE="/tmp/lyon-test-${REPO_NAME}.diff"

git diff "$COMMIT_RANGE" > "$DIFF_FILE"

if [ ! -s "$DIFF_FILE" ]; then
    echo "❌ Error: No diff generated. Possible reasons:"
    echo "   - No changes in the specified range"
    echo "   - Invalid commit range"
    echo ""
    echo "Try:"
    echo "   git log --oneline -5        # See recent commits"
    echo "   git diff HEAD~1 --stat      # Check if there are changes"
    rm -f "$DIFF_FILE"
    exit 1
fi

DIFF_SIZE=$(wc -c < "$DIFF_FILE")
DIFF_LINES=$(wc -l < "$DIFF_FILE")
echo "✓ Diff generated: $DIFF_SIZE bytes, $DIFF_LINES lines"
echo "  Location: $DIFF_FILE"
echo ""

# Show summary of changes
echo "Changes summary:"
git diff "$COMMIT_RANGE" --stat | head -20
echo ""

# Start local HTTP server for diff file
echo "Starting local HTTP server on port 9090..."
cd /tmp || exit 1

# Kill any existing server on port 9090
lsof -ti:9090 | xargs kill -9 2>/dev/null || true

# Start server in background
python3 -m http.server 9090 >/dev/null 2>&1 &
SERVER_PID=$!
echo "✓ Server started (PID: $SERVER_PID)"

# Wait for server to be ready
sleep 1

# Verify server is accessible
DIFF_URL="http://localhost:9090/lyon-test-${REPO_NAME}.diff"
HTTP_STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$DIFF_URL")

if [ "$HTTP_STATUS" != "200" ]; then
    echo "❌ Error: Diff server not accessible (HTTP $HTTP_STATUS)"
    kill $SERVER_PID 2>/dev/null
    exit 1
fi

echo "✓ Diff accessible at: $DIFF_URL"
echo ""

# Trigger Lyon workflow
echo "Triggering Lyon PR review workflow..."

# Go back to Lyon directory
cd - >/dev/null || exit 1

RESPONSE=$(curl -s -X POST http://localhost:8082/webhook/pr \
  -H "Content-Type: application/json" \
  -d "{
    \"action\": \"opened\",
    \"number\": 1,
    \"repository\": {
      \"owner\": {\"login\": \"local\"},
      \"name\": \"${REPO_NAME}\"
    },
    \"pull_request\": {
      \"number\": 1,
      \"title\": \"Local changes from ${COMMIT_RANGE}..HEAD\",
      \"diff_url\": \"${DIFF_URL}\"
    }
  }")

echo ""
echo "Response:"
echo "$RESPONSE" | python3 -m json.tool 2>/dev/null || echo "$RESPONSE"
echo ""

# Extract workflow ID and dashboard URL
WORKFLOW_ID=$(echo "$RESPONSE" | grep -o '"workflow_id":"[^"]*"' | cut -d'"' -f4)
DASHBOARD_URL=$(echo "$RESPONSE" | grep -o '"dashboard_url":"[^"]*"' | cut -d'"' -f4)

if [ -n "$WORKFLOW_ID" ]; then
    echo "✅ Workflow started successfully!"
    echo ""
    echo "📊 Workflow ID: ${WORKFLOW_ID}"
    echo "📊 Dashboard: ${DASHBOARD_URL}"
    echo "🕐 Temporal UI: http://localhost:8080"
    echo ""
    echo "🔍 Watch Lyon logs for OpenRouter API calls:"
    echo "   - 'Sending LLM request'"
    echo "   - 'LLM request completed' (with token counts)"
    echo ""
    echo "The workflow should take 30-60 seconds (4 parallel LLM calls)"
    echo ""
    echo "Opening dashboard in browser..."

    # Open browser (macOS)
    if command -v open &> /dev/null; then
        open "$DASHBOARD_URL"
    fi

    echo ""
    echo "Press Enter when done to stop the HTTP server..."
    read -r

    # Stop server
    kill $SERVER_PID 2>/dev/null
    echo "✓ HTTP server stopped"
    rm -f "$DIFF_FILE"
else
    echo "❌ Failed to start workflow"
    echo ""
    echo "Troubleshooting:"
    echo "  1. Check Lyon is running: curl http://localhost:8082/health"
    echo "  2. Check OPENROUTER_API_KEY is set: ./scripts/check-setup.sh"
    echo "  3. Check Lyon logs for errors"

    # Stop server
    kill $SERVER_PID 2>/dev/null
    rm -f "$DIFF_FILE"
    exit 1
fi
