#!/bin/bash

echo "🔍 Lyon Setup Diagnostic"
echo "========================"
echo ""

# Check if OPENROUTER_API_KEY is set
echo "1. Environment Variables:"
if [ -n "$OPENROUTER_API_KEY" ]; then
    echo "   ✓ OPENROUTER_API_KEY is set (${#OPENROUTER_API_KEY} characters)"
else
    echo "   ✗ OPENROUTER_API_KEY is NOT set"
    echo "     Set it with: export OPENROUTER_API_KEY='your-key-here'"
fi
echo ""

# Check if config.yaml exists and is valid
echo "2. Configuration File:"
if [ -f "config.yaml" ]; then
    echo "   ✓ config.yaml exists"

    # Check if it has the openrouter section
    if grep -q "openrouter:" config.yaml; then
        echo "   ✓ config.yaml has openrouter section"

        # Show base URL
        BASE_URL=$(grep "base_url:" config.yaml | awk '{print $2}' | tr -d '"')
        echo "   ✓ OpenRouter URL: $BASE_URL"
    else
        echo "   ✗ config.yaml missing openrouter section"
    fi
else
    echo "   ✗ config.yaml not found"
fi
echo ""

# Check if prompt files exist
echo "3. Prompt Files:"
PROMPTS_DIR="prompts"
if [ -d "$PROMPTS_DIR" ]; then
    echo "   ✓ prompts/ directory exists"

    for prompt in security.md style.md logic.md documentation.md; do
        if [ -f "$PROMPTS_DIR/$prompt" ]; then
            SIZE=$(wc -c < "$PROMPTS_DIR/$prompt")
            echo "   ✓ $prompt ($SIZE bytes)"
        else
            echo "   ✗ $prompt missing"
        fi
    done
else
    echo "   ✗ prompts/ directory not found"
fi
echo ""

# Check if binary is built
echo "4. Application Binary:"
if [ -f "lyon" ]; then
    BINARY_DATE=$(stat -f "%Sm" -t "%Y-%m-%d %H:%M:%S" lyon 2>/dev/null || stat -c "%y" lyon 2>/dev/null)
    echo "   ✓ lyon binary exists"
    echo "   ✓ Built: $BINARY_DATE"
else
    echo "   ✗ lyon binary not found"
    echo "     Build with: go build"
fi
echo ""

# Check if Temporal is running
echo "5. Temporal Server:"
if curl -s http://localhost:7233 >/dev/null 2>&1; then
    echo "   ✓ Temporal server is reachable on localhost:7233"
else
    echo "   ✗ Temporal server not reachable on localhost:7233"
    echo "     Start with: temporal server start-dev"
fi
echo ""

# Check if Lyon is running
echo "6. Lyon Service:"
if curl -s http://localhost:8082/health >/dev/null 2>&1; then
    echo "   ✓ Lyon service is running on localhost:8082"
else
    echo "   ✗ Lyon service not running on localhost:8082"
    echo "     Start with: ./lyon"
fi
echo ""

echo "================================"
echo "Setup Summary:"
echo ""

if [ -z "$OPENROUTER_API_KEY" ]; then
    echo "⚠️  CRITICAL: Set OPENROUTER_API_KEY environment variable"
    echo ""
    echo "   export OPENROUTER_API_KEY='your-openrouter-key'"
    echo ""
fi

if [ ! -f "lyon" ]; then
    echo "⚠️  Build the application: go build"
    echo ""
fi

if ! curl -s http://localhost:7233 >/dev/null 2>&1; then
    echo "⚠️  Start Temporal: temporal server start-dev"
    echo ""
fi

if ! curl -s http://localhost:8082/health >/dev/null 2>&1; then
    echo "⚠️  Start Lyon: ./lyon"
    echo ""
fi

echo "Once all checks pass, run:"
echo "  ./scripts/test-with-real-pr.sh"
