package observability

import "testing"

func TestContextMetricsRecordsRetrievalIndexAndCompactionSignals(t *testing.T) {
	metrics := &ContextMetrics{}
	metrics.RecordRetrieval(true, true, true)
	metrics.RecordOutbox(true)
	metrics.RecordOutbox(false)
	metrics.RecordOutboxLatency(13)
	metrics.RecordCompaction("fallback")
	metrics.RecordCompaction("failed")
	metrics.RecordConversationSnapshot(true, 100, 30, 1, 25)
	metrics.RecordHistorySearch()
	metrics.RecordContextOverflow()
	snapshot := metrics.Snapshot()
	if snapshot.RetrievalRequests != 1 || snapshot.LowRecall != 1 || snapshot.Clarifications != 1 || snapshot.QueryRewrites != 1 ||
		snapshot.OutboxCompleted != 1 || snapshot.OutboxFailed != 1 || snapshot.OutboxLatencyMS != 13 || snapshot.Compactions != 2 || snapshot.CompactionFallback != 1 || snapshot.CompactionFailed != 1 ||
		snapshot.SnapshotReused != 1 || snapshot.CompactionBeforeTokens != 100 || snapshot.CompactionAfterTokens != 30 || snapshot.CompactionModelCalls != 1 ||
		snapshot.CompactionLatencyMS != 25 || snapshot.HistorySearches != 1 || snapshot.ContextOverflow != 1 {
		t.Fatalf("unexpected metrics: %+v", snapshot)
	}
}

func TestMemoryMetricsSeparatesWriteAndApprovalLatency(t *testing.T) {
	metrics := &MemoryMetrics{}
	metrics.RecordCandidateWrite(7)
	metrics.RecordMemoryApprovalWait(19)
	snapshot := metrics.Snapshot()
	if snapshot["memory_candidate_writes"] != 1 || snapshot["memory_candidate_write_latency_ms"] != 7 || snapshot["memory_approval_decisions"] != 1 || snapshot["memory_approval_wait_ms"] != 19 {
		t.Fatalf("unexpected memory metrics: %+v", snapshot)
	}
}
