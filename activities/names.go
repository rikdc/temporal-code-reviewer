package activities

// Activity name constants used for Temporal activity registration and invocation.
const (
	ActivityDiffFetcher = "DiffFetcher.FetchDiff"
	ActivitySecurity    = "SecurityAgent.Execute"
	ActivityStyle       = "StyleAgent.Execute"
	ActivityLogic       = "LogicAgent.Execute"
	ActivityDocs        = "DocsAgent.Execute"
	ActivitySynthesis   = "SynthesisAgent.Execute"
	ActivityTriage      = "TriageAgent.Execute"
	ActivityGetPRHeadSHA = "GitHubActivity.GetPRHeadSHA"
	ActivityReadFile     = "GitHubActivity.ReadFile"
	ActivityGenerateFix = "FixGeneratorActivity.Execute"
	ActivityCoalesce    = "CoalesceActivity.Execute"
	ActivityCreatePR    = "CreatePRActivity.Execute"
	ActivityListOpenPRs      = "ListPRsActivity.ListOpenPRs"
	ActivityPostReview       = "PostReviewActivity.PostReview"
	ActivityHasPendingReview = "PostReviewActivity.HasPendingReview"
	ActivityCheckFeedback    = "FeedbackPollerActivity.CheckFeedback"
	ActivityHasReviewedAtSHA = "MetricsActivity.HasReviewedAtSHA"
	ActivityRecordSkip       = "MetricsActivity.RecordSkip"
)
