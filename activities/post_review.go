package activities

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/go-github/v68/github"
	"github.com/google/uuid"
	"github.com/rikdc/temporal-code-reviewer/metrics"
	"github.com/rikdc/temporal-code-reviewer/reviews"
	"github.com/rikdc/temporal-code-reviewer/types"
	"go.temporal.io/sdk/activity"
	"go.uber.org/zap"
)

// agentFinding pairs a finding with the name of the agent that produced it.
type agentFinding struct {
	agentName string
	finding   types.Finding
}

// PostReviewActivity posts a draft (PENDING) GitHub PR review containing
// findings from all review agents. Line-specific findings are attached as
// inline comments; all others appear in the review body.
type PostReviewActivity struct {
	client      *github.Client
	logger      *zap.Logger
	store       *reviews.Store    // may be nil; when set, records each posted review
	metricsRepo metrics.Repository // may be nil
}

// NewPostReviewActivity creates a new PostReviewActivity.
// store and metricsRepo may be nil if those features are not required.
func NewPostReviewActivity(client *github.Client, logger *zap.Logger, store *reviews.Store, metricsRepo metrics.Repository) *PostReviewActivity {
	return &PostReviewActivity{client: client, logger: logger, store: store, metricsRepo: metricsRepo}
}

// HasPendingReview returns true if a PENDING (draft) review already exists on
// the PR for the given HEAD SHA. This is used by the poller to skip PRs that
// have already been reviewed at their current commit, even after a worker
// restart where Temporal workflow history has been lost or reused.
func (a *PostReviewActivity) HasPendingReview(ctx context.Context, input types.PRReviewInput) (bool, error) {
	if a.client == nil {
		return false, nil
	}

	reviews, _, err := a.client.PullRequests.ListReviews(
		ctx,
		input.RepoOwner,
		input.RepoName,
		input.PRNumber,
		nil,
	)
	if err != nil {
		return false, fmt.Errorf("list reviews for PR #%d: %w", input.PRNumber, err)
	}

	for _, r := range reviews {
		if r.GetState() == "PENDING" && r.GetCommitID() == input.HeadSHA {
			return true, nil
		}
	}
	return false, nil
}

// PostReview creates a PENDING (draft) GitHub PR review. The user can inspect
// and submit it manually from the GitHub UI.
//
// Any existing PENDING review by the authenticated user is deleted first. This
// prevents reviews becoming permanently inaccessible when new commits are pushed
// after a review is posted — GitHub only shows delete controls for pending reviews
// on the exact commit they were created against, so reviews on stale commits have
// no delete button in the UI.
//
// Before building inline comments we fetch the PR diff and parse which
// (file, line) pairs are actually present in the diff hunks. Findings that
// reference lines outside the diff are placed in the review body instead.
// This prevents GitHub from rejecting the entire request with 422 when an
// LLM-generated line number falls outside the changed hunks.
func (a *PostReviewActivity) PostReview(ctx context.Context, input types.PostReviewInput) error {
	if a.client == nil {
		a.logger.Warn("Skipping GitHub review post — GITHUB_TOKEN not configured")
		return nil
	}

	if err := a.deleteStalePendingReviews(ctx, input.PRReviewInput); err != nil {
		// Non-fatal: log and continue so we still post the new review.
		a.logger.Warn("Could not clean up stale pending reviews",
			zap.Int("pr_number", input.PRReviewInput.PRNumber),
			zap.Error(err))
	}

	// Pre-fetch valid diff lines so we can route findings correctly.
	validLines, err := a.fetchDiffLines(ctx, input.PRReviewInput)
	if err != nil {
		a.logger.Warn("Could not fetch diff for pre-filtering; all findings will go to body",
			zap.Int("pr_number", input.PRReviewInput.PRNumber),
			zap.Error(err))
	}

	var lineComments []*github.DraftReviewComment
	var bodyFindings []agentFinding

	for _, result := range input.AgentResults {
		for _, f := range result.StructuredFindings {
			// Skip parse-failure placeholders — they contain raw LLM output
			// and are not useful as review comments.
			if f.Title == "Raw LLM Response" {
				continue
			}
			if validLines != nil && f.File != "" && f.Line > 0 && validLines[f.File][f.Line] {
				lineComments = append(lineComments, &github.DraftReviewComment{
					Path: github.String(f.File),
					Line: github.Int(f.Line),
					Side: github.String("RIGHT"),
					Body: github.String(formatLineComment(result.AgentName, f)),
				})
			} else {
				bodyFindings = append(bodyFindings, agentFinding{result.AgentName, f})
			}
		}
	}

	body := formatReviewBody(input.Summary, bodyFindings)
	ghReview, err := a.createReview(ctx, input, body, lineComments)
	if err != nil {
		return fmt.Errorf("post GitHub review for PR #%d: %w", input.PRReviewInput.PRNumber, err)
	}

	a.logger.Info("Posted draft GitHub review",
		zap.String("repo", input.PRReviewInput.RepoOwner+"/"+input.PRReviewInput.RepoName),
		zap.Int("pr_number", input.PRReviewInput.PRNumber),
		zap.Int("inline_comments", len(lineComments)),
		zap.Int("body_findings", len(bodyFindings)))

	if a.store != nil {
		a.store.Add(input)
	}

	if a.metricsRepo != nil {
		workflowID := activity.GetInfo(ctx).WorkflowExecution.ID
		a.saveMetrics(ctx, workflowID, input, ghReview.GetID())
	}

	return nil
}

// saveMetrics persists the review run and its findings to the metrics store.
// Errors are logged but do not fail the activity.
func (a *PostReviewActivity) saveMetrics(ctx context.Context, workflowID string, input types.PostReviewInput, githubReviewID int64) {
	pr := input.PRReviewInput

	if err := a.metricsRepo.SaveReviewRun(ctx, metrics.ReviewRun{
		ID:             workflowID,
		PRNumber:       pr.PRNumber,
		RepoOwner:      pr.RepoOwner,
		RepoName:       pr.RepoName,
		HeadSHA:        pr.HeadSHA,
		GitHubReviewID: githubReviewID,
	}); err != nil {
		a.logger.Warn("Failed to save review run", zap.Error(err))
		return
	}

	// Fetch GitHub comment IDs so we can match deleted comments later.
	ghComments, _, err := a.client.PullRequests.ListReviewComments(
		ctx, pr.RepoOwner, pr.RepoName, pr.PRNumber, githubReviewID, nil,
	)
	if err != nil {
		a.logger.Warn("Failed to list review comments for metrics", zap.Error(err))
	}

	// Build a lookup: (file, line) → GitHub comment ID
	type fileLineKey struct{ file string; line int }
	commentIDs := make(map[fileLineKey]int64, len(ghComments))
	for _, c := range ghComments {
		commentIDs[fileLineKey{c.GetPath(), c.GetLine()}] = c.GetID()
	}

	var findingRecords []metrics.FindingRecord
	for _, result := range input.AgentResults {
		// Look up the agent_run UUID saved by ReviewAgent.Execute.
		agentRunID, found, err := a.metricsRepo.GetAgentRunID(ctx, workflowID, result.AgentName)
		if err != nil || !found {
			a.logger.Warn("Agent run not found for findings; skipping",
				zap.String("agent", result.AgentName), zap.Error(err))
			continue
		}
		for _, f := range result.StructuredFindings {
			if f.Title == "Raw LLM Response" {
				continue
			}
			ghCommentID := commentIDs[fileLineKey{f.File, f.Line}]
			findingRecords = append(findingRecords, metrics.FindingRecord{
				ID:              uuid.New().String(),
				AgentRunID:      agentRunID,
				Severity:        f.Severity,
				Title:           f.Title,
				FilePath:        f.File,
				LineNumber:      f.Line,
				GitHubCommentID: ghCommentID,
			})
		}
	}

	if len(findingRecords) > 0 {
		if err := a.metricsRepo.SaveFindings(ctx, findingRecords); err != nil {
			a.logger.Warn("Failed to save findings", zap.Error(err))
		}
	}
}

// fetchDiffLines fetches the unified diff for a PR and returns a map of
// file path → set of new-file line numbers present in the diff hunks.
func (a *PostReviewActivity) fetchDiffLines(ctx context.Context, pr types.PRReviewInput) (map[string]map[int]bool, error) {
	diff, _, err := a.client.PullRequests.GetRaw(
		ctx,
		pr.RepoOwner,
		pr.RepoName,
		pr.PRNumber,
		github.RawOptions{Type: github.Diff},
	)
	if err != nil {
		return nil, fmt.Errorf("fetch diff: %w", err)
	}
	return parseNewFileLines(diff), nil
}

// parseNewFileLines parses a unified diff and returns a map of file path to
// the set of new-file line numbers that appear in the diff (additions and
// context lines). Only these lines are valid targets for GitHub inline review
// comments.
func parseNewFileLines(diff string) map[string]map[int]bool {
	result := make(map[string]map[int]bool)
	var currentFile string
	newLineNum := 0

	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++ b/"):
			currentFile = strings.TrimPrefix(line, "+++ b/")
			if result[currentFile] == nil {
				result[currentFile] = make(map[int]bool)
			}
			newLineNum = 0
		case strings.HasPrefix(line, "@@"):
			newLineNum = parseNewFileHunkStart(line)
		case currentFile == "" || newLineNum == 0:
			// Haven't entered a hunk yet.
		case strings.HasPrefix(line, "+"):
			result[currentFile][newLineNum] = true
			newLineNum++
		case strings.HasPrefix(line, " "):
			result[currentFile][newLineNum] = true
			newLineNum++
		case strings.HasPrefix(line, "-"):
			// Deletion — old-file line only, new line number does not advance.
		}
	}

	return result
}

// parseNewFileHunkStart extracts the new-file starting line number from a
// unified diff hunk header ("@@ -l,s +l,s @@ ..."). Returns 0 on failure.
func parseNewFileHunkStart(line string) int {
	idx := strings.Index(line, " +")
	if idx < 0 {
		return 0
	}
	rest := line[idx+2:] // "l,s @@ ..." or "l @@..."
	end := strings.IndexAny(rest, ", ")
	if end < 0 {
		end = len(rest)
	}
	n, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0
	}
	return n
}

// deleteStalePendingReviews deletes any PENDING reviews on the PR authored by
// the authenticated user. A pending review on a stale commit has no delete
// button in the GitHub UI, so we clean up programmatically before posting.
func (a *PostReviewActivity) deleteStalePendingReviews(ctx context.Context, pr types.PRReviewInput) error {
	me, _, err := a.client.Users.Get(ctx, "")
	if err != nil {
		return fmt.Errorf("get authenticated user: %w", err)
	}
	myLogin := me.GetLogin()

	reviews, _, err := a.client.PullRequests.ListReviews(ctx, pr.RepoOwner, pr.RepoName, pr.PRNumber, nil)
	if err != nil {
		return fmt.Errorf("list reviews: %w", err)
	}

	for _, r := range reviews {
		if r.GetState() != "PENDING" || r.GetUser().GetLogin() != myLogin {
			continue
		}
		if _, _, err := a.client.PullRequests.DeletePendingReview(ctx, pr.RepoOwner, pr.RepoName, pr.PRNumber, r.GetID()); err != nil {
			a.logger.Warn("Failed to delete stale pending review",
				zap.Int64("review_id", r.GetID()),
				zap.String("commit_id", r.GetCommitID()),
				zap.Error(err))
		} else {
			a.logger.Info("Deleted stale pending review",
				zap.Int64("review_id", r.GetID()),
				zap.String("commit_id", r.GetCommitID()))
		}
	}
	return nil
}

func (a *PostReviewActivity) createReview(
	ctx context.Context,
	input types.PostReviewInput,
	body string,
	comments []*github.DraftReviewComment,
) (*github.PullRequestReview, error) {
	req := &github.PullRequestReviewRequest{
		Body:     github.String(body),
		Comments: comments,
		// Omitting Event creates a PENDING (draft) review the user submits manually.
	}
	if input.PRReviewInput.HeadSHA != "" {
		req.CommitID = github.String(input.PRReviewInput.HeadSHA)
	}

	review, _, err := a.client.PullRequests.CreateReview(
		ctx,
		input.PRReviewInput.RepoOwner,
		input.PRReviewInput.RepoName,
		input.PRReviewInput.PRNumber,
		req,
	)
	return review, err
}

// formatLineComment formats a single finding as a GitHub inline comment.
// No header or title — just the humanized description, with any non-code
// suggested fix folded into the prose and code fixes rendered as a block.
func formatLineComment(_ string, f types.Finding) string {
	var sb strings.Builder

	description := humanizeText(f.Description)
	fix := strings.TrimSpace(f.SuggestedFix)

	if fix != "" && !looksLikeCode(fix) {
		// Prose fix: fold into the description rather than a separate block.
		description = strings.TrimRight(description, " \n") + " " + humanizeText(fix)
		fix = ""
	}

	if description != "" {
		sb.WriteString(description)
		sb.WriteString("\n")
	}

	if fix != "" {
		sb.WriteString("\n```go\n")
		sb.WriteString(fix)
		sb.WriteString("\n```")
	}

	return sb.String()
}

// formatReviewBody builds the top-level review body from the synthesis summary
// and any findings that could not be attached to a specific line.
func formatReviewBody(summary types.ReviewSummary, findings []agentFinding) string {
	var sb strings.Builder

	sb.WriteString("## Code Review\n\n")
	fmt.Fprintf(&sb, "**Overall:** %s\n\n", summary.OverallStatus)

	if summary.Summary != "" {
		sb.WriteString(humanizeText(summary.Summary))
		sb.WriteString("\n\n")
	}
	if summary.Recommendation != "" {
		fmt.Fprintf(&sb, "**Recommendation:** %s\n\n", humanizeText(summary.Recommendation))
	}

	if len(findings) > 0 {
		sb.WriteString("---\n\n### Additional findings\n\n")
		for _, af := range findings {
			// Show file:line as a minimal locator — no agent label or bold title.
			if af.finding.File != "" {
				fmt.Fprintf(&sb, "`%s`", af.finding.File)
				if af.finding.Line > 0 {
					fmt.Fprintf(&sb, " line %d", af.finding.Line)
				}
				sb.WriteString("\n\n")
			}

			description := humanizeText(af.finding.Description)
			fix := strings.TrimSpace(af.finding.SuggestedFix)

			if fix != "" && !looksLikeCode(fix) {
				description = strings.TrimRight(description, " \n") + " " + humanizeText(fix)
				fix = ""
			}

			if description != "" {
				sb.WriteString(description)
				sb.WriteString("\n\n")
			}
			if fix != "" {
				sb.WriteString("```go\n")
				sb.WriteString(fix)
				sb.WriteString("\n```\n\n")
			}
		}
	}

	sb.WriteString("---\n*Review generated automatically. Inspect findings and submit when ready.*")
	return sb.String()
}

// humanizeText strips common AI writing patterns from s: em-dashes, filler
// phrases, and over-formal constructions that make generated text feel robotic.
func humanizeText(s string) string {
	// Ordered replacements — longer phrases before their sub-strings.
	r := strings.NewReplacer(
		// Em-dashes (U+2014) → comma or nothing depending on spacing.
		" — ", ", ",
		"— ", ", ",
		" —", ",",
		"—", ", ",

		// Filler openers that add no information.
		"It's worth noting that ", "",
		"it's worth noting that ", "",
		"It is worth noting that ", "",
		"it is worth noting that ", "",
		"It's important to note that ", "",
		"it's important to note that ", "",
		"It is important to note that ", "",
		"it is important to note that ", "",
		"Note that ", "",
		"note that ", "",

		// Transitional padding.
		"Additionally, ", "",
		"Additionally ", "",
		"Furthermore, ", "",
		"Furthermore ", "",
		"Moreover, ", "",
		"Moreover ", "",
		"In addition, ", "",
		"In addition ", "",
		"Moving forward, ", "",
		"moving forward, ", "",
		"At the end of the day, ", "",
		"at the end of the day, ", "",

		// Unnecessarily formal verbs.
		"Leverage ", "Use ",
		"leverage ", "use ",
		"leverages ", "uses ",
		"Leverages ", "Uses ",
		"Utilize ", "Use ",
		"utilize ", "use ",
		"utilizes ", "uses ",
		"Utilizes ", "Uses ",
		"utilized ", "used ",
		"Utilized ", "Used ",

		// "Ensure" is fine in technical writing but "make sure" reads more naturally.
		"Ensure that ", "Make sure ",
		"ensure that ", "make sure ",
		"Ensure ", "Make sure ",
		"ensure ", "make sure ",

		// Verbose constructions.
		"in order to ", "to ",
		"In order to ", "To ",
		"this approach ", "this ",
		"This approach ", "This ",
		"This can be done by ", "",
		"this can be done by ", "",
	)
	return r.Replace(s)
}

// looksLikeCode returns true if s appears to be a code snippet rather than
// prose. Multi-line text or text containing common Go syntax tokens is treated
// as code and rendered in a fenced block; everything else is folded into the
// comment prose.
func looksLikeCode(s string) bool {
	if strings.Contains(s, "\n") {
		return true
	}
	for _, tok := range []string{
		":=", "func ", "var ", "const ", "type ", "return ",
		"if ", "for ", "package ", "import ", "// ",
	} {
		if strings.Contains(s, tok) {
			return true
		}
	}
	return false
}
