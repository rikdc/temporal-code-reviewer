#!/bin/bash

# Demo script to trigger a PR review workflow

echo "🦁 Lyon PR Review Demo Trigger"
echo "================================"
echo ""

# Default values
PR_NUMBER=${1:-123}
REPO_OWNER=${2:-example}
REPO_NAME=${3:-test-repo}
PR_TITLE=${4:-"Add new feature"}

echo "Triggering PR review for:"
echo "  Repository: ${REPO_OWNER}/${REPO_NAME}"
echo "  PR Number: ${PR_NUMBER}"
echo "  Title: ${PR_TITLE}"
echo ""

# Send webhook request
RESPONSE=$(curl -s -X POST http://localhost:8082/webhook/pr \
  -H "Content-Type: application/json" \
  -d "{
    \"action\": \"opened\",
    \"number\": ${PR_NUMBER},
    \"repository\": {
      \"owner\": {\"login\": \"${REPO_OWNER}\"},
      \"name\": \"${REPO_NAME}\"
    },
    \"pull_request\": {
      \"number\": ${PR_NUMBER},
      \"title\": \"${PR_TITLE}\",
      \"diff_url\": \"https://github.com/${REPO_OWNER}/${REPO_NAME}/pull/${PR_NUMBER}.diff\"
    }
  }")

echo "Response:"
echo "$RESPONSE" | python3 -m json.tool 2>/dev/null || echo "$RESPONSE"
echo ""

# Extract dashboard URL
DASHBOARD_URL=$(echo "$RESPONSE" | grep -o '"dashboard_url":"[^"]*"' | cut -d'"' -f4)

if [ -n "$DASHBOARD_URL" ]; then
    echo "✅ Workflow started successfully!"
    echo ""
    echo "📊 View dashboard: ${DASHBOARD_URL}"
    echo "🕐 View Temporal UI: http://localhost:8080"
    echo ""
    echo "Opening dashboard in browser..."

    # Try to open browser (macOS)
    if command -v open &> /dev/null; then
        open "$DASHBOARD_URL"
    elif command -v xdg-open &> /dev/null; then
        xdg-open "$DASHBOARD_URL"
    else
        echo "Please open the URL manually in your browser."
    fi
else
    echo "❌ Failed to start workflow"
fi
