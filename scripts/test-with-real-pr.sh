#!/bin/bash

# Test Lyon with a real GitHub PR that has actual code changes

echo "🦁 Lyon PR Review - Real PR Test"
echo "=================================="
echo ""

# Use Lyon's own PR #1 as test
REPO_OWNER="rikdc"
REPO_NAME="conductor-playground"
PR_NUMBER="1"

echo "Testing with real PR:"
echo "  Repository: ${REPO_OWNER}/${REPO_NAME}"
echo "  PR: #${PR_NUMBER}"
echo "  URL: https://github.com/${REPO_OWNER}/${REPO_NAME}/pull/${PR_NUMBER}"
echo ""
echo "This PR contains the initial Lyon implementation with real code changes."
echo ""

# First, verify the diff URL is accessible
DIFF_URL="https://github.com/${REPO_OWNER}/${REPO_NAME}/pull/${PR_NUMBER}.diff"
echo "Checking if diff is accessible..."
DIFF_STATUS=$(curl -s -o /dev/null -w "%{http_code}" "$DIFF_URL")

if [ "$DIFF_STATUS" = "200" ]; then
    echo "✓ Diff URL is accessible (HTTP $DIFF_STATUS)"
    echo ""
else
    echo "✗ Diff URL returned HTTP $DIFF_STATUS"
    echo "  URL: $DIFF_URL"
    exit 1
fi

# Send webhook request
echo "Triggering PR review workflow..."
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
      \"title\": \"Implement Lyon Multi-Agent PR Review System with Temporal\",
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
    echo "🔍 Watch for OpenRouter API calls in the logs..."
    echo "   Look for: 'Sending LLM request' and 'LLM request completed'"
    echo ""
    echo "Opening dashboard in browser..."

    # Open browser (macOS)
    if command -v open &> /dev/null; then
        open "$DASHBOARD_URL"
    fi
else
    echo "❌ Failed to start workflow"
    echo "Check that Lyon service is running on http://localhost:8082"
fi
