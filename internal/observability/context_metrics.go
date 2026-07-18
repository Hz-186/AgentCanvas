package observability

import "sync/atomic"

type ContextMetricSnapshot struct {
	RetrievalRequests  uint64 `json:"retrieval_requests"`
	LowRecall          uint64 `json:"low_recall"`
	Clarifications     uint64 `json:"clarifications"`
	QueryRewrites      uint64 `json:"query_rewrites"`
	OutboxCompleted    uint64 `json:"outbox_completed"`
	OutboxFailed       uint64 `json:"outbox_failed"`
	Compactions        uint64 `json:"compactions"`
	CompactionFallback uint64 `json:"compaction_fallback"`
	CompactionFailed   uint64 `json:"compaction_failed"`
	ContextOverflow    uint64 `json:"context_overflow"`
}

type ContextMetrics struct {
	retrievalRequests  atomic.Uint64
	lowRecall          atomic.Uint64
	clarifications     atomic.Uint64
	queryRewrites      atomic.Uint64
	outboxCompleted    atomic.Uint64
	outboxFailed       atomic.Uint64
	compactions        atomic.Uint64
	compactionFallback atomic.Uint64
	compactionFailed   atomic.Uint64
	contextOverflow    atomic.Uint64
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

func (m *ContextMetrics) RecordCompaction(status string) {
	m.compactions.Add(1)
	switch status {
	case "fallback":
		m.compactionFallback.Add(1)
	case "failed":
		m.compactionFailed.Add(1)
	}
}

func (m *ContextMetrics) RecordContextOverflow() { m.contextOverflow.Add(1) }

func (m *ContextMetrics) Snapshot() ContextMetricSnapshot {
	return ContextMetricSnapshot{
		RetrievalRequests: m.retrievalRequests.Load(), LowRecall: m.lowRecall.Load(), Clarifications: m.clarifications.Load(), QueryRewrites: m.queryRewrites.Load(),
		OutboxCompleted: m.outboxCompleted.Load(), OutboxFailed: m.outboxFailed.Load(), Compactions: m.compactions.Load(),
		CompactionFallback: m.compactionFallback.Load(), CompactionFailed: m.compactionFailed.Load(), ContextOverflow: m.contextOverflow.Load(),
	}
}
