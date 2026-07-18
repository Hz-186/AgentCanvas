package observability

import "testing"

func TestContextMetricsRecordsRetrievalIndexAndCompactionSignals(t *testing.T) {
	metrics := &ContextMetrics{}
	metrics.RecordRetrieval(true, true, true)
	metrics.RecordOutbox(true)
	metrics.RecordOutbox(false)
	metrics.RecordCompaction("fallback")
	metrics.RecordCompaction("failed")
	metrics.RecordContextOverflow()
	snapshot := metrics.Snapshot()
	if snapshot.RetrievalRequests != 1 || snapshot.LowRecall != 1 || snapshot.Clarifications != 1 || snapshot.QueryRewrites != 1 ||
		snapshot.OutboxCompleted != 1 || snapshot.OutboxFailed != 1 || snapshot.Compactions != 2 || snapshot.CompactionFallback != 1 || snapshot.CompactionFailed != 1 || snapshot.ContextOverflow != 1 {
		t.Fatalf("unexpected metrics: %+v", snapshot)
	}
}
