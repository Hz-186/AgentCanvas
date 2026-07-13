package observability

import "testing"

func TestReflectionMetricsRecordsLifecycle(t *testing.T) {
	metrics := &ReflectionMetrics{}
	metrics.RecordRecall(true, 2, 40, true)
	metrics.RecordRecallFailure()
	metrics.RecordStored(false)
	metrics.RecordStored(true)
	metrics.RecordJobEnqueued()
	metrics.RecordJobCompleted()
	metrics.RecordJobFailure(true)
	metrics.RecordJobFailure(false)
	metrics.RecordInlineTriggered()
	metrics.RecordInlineCompleted()
	metrics.RecordInlineFailed()
	metrics.RecordFeedback("helpful")
	metrics.RecordFeedback("harmful")

	snapshot := metrics.Snapshot()
	if snapshot.RecallRequests != 1 || snapshot.RecallHits != 1 || snapshot.ShadowRecallRequests != 1 ||
		snapshot.RecalledLessons != 2 || snapshot.RecalledTokens != 40 || snapshot.RecallFailures != 1 ||
		snapshot.Stored != 1 || snapshot.Deduplicated != 1 || snapshot.JobsEnqueued != 1 ||
		snapshot.JobsCompleted != 1 || snapshot.JobsRetried != 1 || snapshot.JobsFailed != 1 ||
		snapshot.InlineTriggered != 1 || snapshot.InlineCompleted != 1 || snapshot.InlineFailed != 1 ||
		snapshot.FeedbackHelpful != 1 || snapshot.FeedbackHarmful != 1 {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}
}
