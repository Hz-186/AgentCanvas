package observability

import "sync/atomic"

type ContextMetricSnapshot struct {
	RetrievalRequests      uint64 `json:"retrieval_requests"`
	LowRecall              uint64 `json:"low_recall"`
	Clarifications         uint64 `json:"clarifications"`
	QueryRewrites          uint64 `json:"query_rewrites"`
	OutboxCompleted        uint64 `json:"outbox_completed"`
	OutboxFailed           uint64 `json:"outbox_failed"`
	OutboxLatencyMS        uint64 `json:"outbox_latency_ms"`
	Compactions            uint64 `json:"compactions"`
	CompactionFallback     uint64 `json:"compaction_fallback"`
	CompactionFailed       uint64 `json:"compaction_failed"`
	SnapshotReused         uint64 `json:"snapshot_reused"`
	CompactionBeforeTokens uint64 `json:"compaction_before_tokens"`
	CompactionAfterTokens  uint64 `json:"compaction_after_tokens"`
	CompactionModelCalls   uint64 `json:"compaction_model_calls"`
	CompactionLatencyMS    uint64 `json:"compaction_latency_ms"`
	HistorySearches        uint64 `json:"history_searches"`
	ContextOverflow        uint64 `json:"context_overflow"`
}

type ContextMetrics struct {
	retrievalRequests      atomic.Uint64
	lowRecall              atomic.Uint64
	clarifications         atomic.Uint64
	queryRewrites          atomic.Uint64
	outboxCompleted        atomic.Uint64
	outboxFailed           atomic.Uint64
	outboxLatencyMS        atomic.Uint64
	compactions            atomic.Uint64
	compactionFallback     atomic.Uint64
	compactionFailed       atomic.Uint64
	snapshotReused         atomic.Uint64
	compactionBeforeTokens atomic.Uint64
	compactionAfterTokens  atomic.Uint64
	compactionModelCalls   atomic.Uint64
	compactionLatencyMS    atomic.Uint64
	historySearches        atomic.Uint64
	contextOverflow        atomic.Uint64
}

var ContextSystemMetrics = &ContextMetrics{}

func (m *ContextMetrics) RecordRetrieval(lowRecall, clarification, rewrite bool) {
	m.retrievalRequests.Add(1)
	if lowRecall {
		m.lowRecall.Add(1)
	}
	if clarification {
		m.clarifications.Add(1)
	}
	if rewrite {
		m.queryRewrites.Add(1)
	}
}

func (m *ContextMetrics) RecordOutbox(success bool) {
	if success {
		m.outboxCompleted.Add(1)
	} else {
		m.outboxFailed.Add(1)
	}
}

func (m *ContextMetrics) RecordOutboxLatency(latencyMS int64) {
	if latencyMS > 0 {
		m.outboxLatencyMS.Add(uint64(latencyMS))
	}
}

func (m *ContextMetrics) RecordCompaction(status string) {
	m.compactions.Add(1)
	switch status {
	case "fallback":
		m.compactionFallback.Add(1)
	case "failed":
		m.compactionFailed.Add(1)
	}
}

func (m *ContextMetrics) RecordConversationSnapshot(reused bool, beforeTokens, afterTokens, modelCalls int, latencyMS int64) {
	if reused {
		m.snapshotReused.Add(1)
	}
	if beforeTokens > 0 {
		m.compactionBeforeTokens.Add(uint64(beforeTokens))
	}
	if afterTokens > 0 {
		m.compactionAfterTokens.Add(uint64(afterTokens))
	}
	if modelCalls > 0 {
		m.compactionModelCalls.Add(uint64(modelCalls))
	}
	if latencyMS > 0 {
		m.compactionLatencyMS.Add(uint64(latencyMS))
	}
}

func (m *ContextMetrics) RecordHistorySearch() { m.historySearches.Add(1) }

func (m *ContextMetrics) RecordContextOverflow() { m.contextOverflow.Add(1) }

func (m *ContextMetrics) Snapshot() ContextMetricSnapshot {
	return ContextMetricSnapshot{
		RetrievalRequests: m.retrievalRequests.Load(), LowRecall: m.lowRecall.Load(), Clarifications: m.clarifications.Load(), QueryRewrites: m.queryRewrites.Load(),
		OutboxCompleted: m.outboxCompleted.Load(), OutboxFailed: m.outboxFailed.Load(), OutboxLatencyMS: m.outboxLatencyMS.Load(), Compactions: m.compactions.Load(),
		CompactionFallback: m.compactionFallback.Load(), CompactionFailed: m.compactionFailed.Load(), SnapshotReused: m.snapshotReused.Load(),
		CompactionBeforeTokens: m.compactionBeforeTokens.Load(), CompactionAfterTokens: m.compactionAfterTokens.Load(),
		CompactionModelCalls: m.compactionModelCalls.Load(), CompactionLatencyMS: m.compactionLatencyMS.Load(), HistorySearches: m.historySearches.Load(), ContextOverflow: m.contextOverflow.Load(),
	}
}
