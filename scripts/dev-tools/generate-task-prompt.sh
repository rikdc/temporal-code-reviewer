#!/bin/bash
# Generate implementation prompt for a specific Beads issue

set -e

if [ -z "$1" ]; then
  echo "Usage: ./generate-task-prompt.sh <ISSUE_ID>"
  echo "Example: ./generate-task-prompt.sh LYON-i08"
  exit 1
fi

ISSUE_ID="$1"

# Get issue details from Beads
ISSUE_DATA=$(bd show "$ISSUE_ID" 2>/dev/null)

if [ $? -ne 0 ]; then
  echo "Error: Issue $ISSUE_ID not found"
  exit 1
fi

# Extract fields (this is a simplified extraction - adjust based on bd show output format)
TITLE=$(echo "$ISSUE_DATA" | grep -A1 "^○" | tail -1 | sed 's/.*· //' | sed 's/ \[.*//')
PRIORITY=$(echo "$ISSUE_DATA" | grep -o "P[0-9]" | head -1)
TYPE=$(echo "$ISSUE_DATA" | grep "Type:" | sed 's/.*Type: //' | awk '{print $1}')

# Get description and notes sections
DESCRIPTION=$(echo "$ISSUE_DATA" | sed -n '/DESCRIPTION/,/NOTES/p' | grep -v "DESCRIPTION" | grep -v "NOTES" | sed '/^$/d')
NOTES=$(echo "$ISSUE_DATA" | sed -n '/NOTES/,/BLOCKS/p' | grep -v "NOTES" | grep -v "BLOCKS" | sed '/^$/d')

# Check dependencies
BLOCKED_BY=$(echo "$ISSUE_DATA" | grep "BLOCKED BY" -A10 || echo "None")
if echo "$BLOCKED_BY" | grep -q "←"; then
  DEPS_STATUS="⚠️  BLOCKED - Dependencies must be completed first:
$BLOCKED_BY"
else
  DEPS_STATUS="✅ READY (No blockers)"
fi

# Generate the prompt
cat <<EOF
You are implementing a Beads-tracked task for the Lyon Multi-Agent PR Review System.

# Task Details

**Issue ID**: $ISSUE_ID
**Title**: $TITLE
**Priority**: $PRIORITY
**Type**: $TYPE

## Description
$DESCRIPTION

## Expected Outcome
$NOTES

## Dependencies
$DEPS_STATUS

# Implementation Requirements

## 1. Pre-Implementation Checklist
- [ ] Read the task description and expected outcome carefully
- [ ] Verify all dependencies are completed: \`bd show $ISSUE_ID\`
- [ ] Review relevant design documents:
  - @docs/IMPLEMENTATION_PLAN.md
  - @docs/llm-integration-plan-v3-simplified.md
  - @docs/task-breakdown.json
- [ ] Mark task as in progress: \`bd update $ISSUE_ID --status in_progress\`

## 2. Implementation Guidelines

### Code Quality Standards
- Follow Go best practices and coding standards
- Use structured logging with zap
- Implement proper error handling with context wrapping
- Add JSON tags for all serializable structs
- Keep functions focused and testable

### Testing Requirements (MANDATORY)
- **Unit Tests**: Test all new functions with table-driven tests
- **Edge Cases**: Test error conditions, nil inputs, empty values
- **Integration Tests**: Test component interaction where applicable
- **Test Coverage**: Aim for 80%+ coverage on new code
- **Test Independence**: Tests must run independently and in parallel

### Testing Checklist
- [ ] All new functions have unit tests
- [ ] Error cases are tested
- [ ] Edge cases are covered (nil, empty, invalid input)
- [ ] Tests pass: \`go test ./... -v\`
- [ ] Tests run in parallel: \`go test ./... -race\`
- [ ] No test flakiness (run tests 3 times)
- [ ] golangci-lint passes: \`golangci-lint run\`

## 3. Implementation Steps

1. **Read Context**
   - Use Read tool to examine related files mentioned in dependencies
   - Understand existing patterns in the codebase
   - Review similar implementations (e.g., other agents)

2. **Implement Code**
   - Create new files or modify existing ones as specified
   - Follow existing patterns and conventions
   - Add comprehensive error handling
   - Include structured logging at key points

3. **Write Tests FIRST or ALONGSIDE**
   - Create *_test.go files for new code
   - Use table-driven test pattern
   - Mock external dependencies (LLM, HTTP, cache)
   - Test happy path and error conditions

4. **Validate Implementation**
   - Run: \`go test ./... -v -race\`
   - Run: \`golangci-lint run\`
   - Run: \`go build\` (ensure no compilation errors)
   - Review test output for proper coverage

## 4. Completion Checklist

Before marking the task as complete, verify:

- [ ] All code files mentioned in task are created/modified
- [ ] Code follows Go best practices (early returns, structured errors)
- [ ] All functions have proper error handling
- [ ] Structured logging added at key points (zap.Logger)
- [ ] Unit tests created for all new functions
- [ ] Integration tests created if specified
- [ ] All tests pass: \`go test ./...\`
- [ ] No race conditions: \`go test ./... -race\`
- [ ] Linting passes: \`golangci-lint run\`
- [ ] Code compiles: \`go build\`
- [ ] Expected output from task notes is achieved
- [ ] No TODO comments or incomplete implementations
- [ ] Dependencies in go.mod are added if needed

## 5. Completion Steps

Once all checks pass:

\`\`\`bash
# 1. Verify tests pass
go test ./... -v

# 2. Verify linting passes
golangci-lint run

# 3. Check git status
git status

# 4. Stage changes
git add <modified-files>

# 5. Commit with descriptive message
git commit -m "feat: $TITLE

Implements $ISSUE_ID

Tests: Added unit tests with comprehensive coverage"

# 6. Mark issue complete
bd close $ISSUE_ID

# 7. Sync beads changes
bd sync

# 8. Check what's newly available
bd ready
\`\`\`

## 6. Reporting

After completion, report to the user:

1. **What was implemented**: Brief summary of changes
2. **Files created/modified**: List of all files changed
3. **Test results**: Output from \`go test\` showing passes
4. **Coverage**: Approximate test coverage percentage
5. **Next available tasks**: Output from \`bd ready\`

## 7. Quality Gates (MUST PASS)

Your implementation is NOT complete until:

✅ Code compiles without errors
✅ All tests pass (no skipped tests)
✅ No race conditions detected
✅ golangci-lint reports zero errors
✅ Test coverage ≥ 80% for new code
✅ All expected files from task description exist
✅ Code follows existing patterns in codebase
✅ Error handling is comprehensive
✅ Logging is structured and informative

## 8. Success Criteria

A task is successfully completed when:

1. ✅ All code specified in task description is implemented
2. ✅ All tests pass with ≥80% coverage
3. ✅ Code quality checks pass (lint, build, race)
4. ✅ Expected outcome from notes is achieved
5. ✅ Changes are committed to git
6. ✅ Issue is closed in Beads
7. ✅ Beads changes are synced
8. ✅ User is informed of completion and next steps

---

**IMPORTANT**: Do not skip testing. Do not mark the task complete until all quality gates pass.
Work systematically through the implementation steps and completion checklist.
EOF
