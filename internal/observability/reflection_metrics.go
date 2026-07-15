package observability

import (
	"sync/atomic"
	"time"
)

type ReflectionMetricSnapshot struct {
	RecallRequests       uint64 `json:"recall_requests"`
	RecallHits           uint64 `json:"recall_hits"`
	RecallFailures       uint64 `json:"recall_failures"`
	ShadowRecallRequests uint64 `json:"shadow_recall_requests"`
	RecalledLessons      uint64 `json:"recalled_lessons"`
	RecalledTokens       uint64 `json:"recalled_tokens"`
	Stored               uint64 `json:"stored"`
	Deduplicated         uint64 `json:"deduplicated"`
	JobsEnqueued         uint64 `json:"jobs_enqueued"`
	JobsCompleted        uint64 `json:"jobs_completed"`
	JobsFailed           uint64 `json:"jobs_failed"`
	JobsRetried          uint64 `json:"jobs_retried"`
	JobsEnqueueFailed    uint64 `json:"jobs_enqueue_failed"`
	HeartbeatFailures    uint64 `json:"heartbeat_failures"`
	LeaseConflicts       uint64 `json:"lease_conflicts"`
	OutboxPublished      uint64 `json:"outbox_published"`
	OutboxPublishFailed  uint64 `json:"outbox_publish_failed"`
	DLQJobs              uint64 `json:"dlq_jobs"`
	MessagesRedelivered  uint64 `json:"messages_redelivered"`
	ProcessingLatencyMS  uint64 `json:"processing_latency_ms"`
	PublishLatencyMS     uint64 `json:"publish_latency_ms"`
	InlineTriggered      uint64 `json:"inline_triggered"`
	InlineCompleted      uint64 `json:"inline_completed"`
	InlineFailed         uint64 `json:"inline_failed"`
	FeedbackHelpful      uint64 `json:"feedback_helpful"`
	FeedbackHarmful      uint64 `json:"feedback_harmful"`
}

type ReflectionMetrics struct {
	recallRequests       atomic.Uint64
	recallHits           atomic.Uint64
	recallFailures       atomic.Uint64
	shadowRecallRequests atomic.Uint64
	recalledLessons      atomic.Uint64
	recalledTokens       atomic.Uint64
	stored               atomic.Uint64
	deduplicated         atomic.Uint64
	jobsEnqueued         atomic.Uint64
	jobsCompleted        atomic.Uint64
	jobsFailed           atomic.Uint64
	jobsRetried          atomic.Uint64
	jobsEnqueueFailed    atomic.Uint64
	heartbeatFailures    atomic.Uint64
	leaseConflicts       atomic.Uint64
	outboxPublished      atomic.Uint64
	outboxPublishFailed  atomic.Uint64
	dlqJobs              atomic.Uint64
	messagesRedelivered  atomic.Uint64
	processingLatencyNS  atomic.Uint64
	processingCount      atomic.Uint64
	publishLatencyNS     atomic.Uint64
	publishCount         atomic.Uint64
	inlineTriggered      atomic.Uint64
	inlineCompleted      atomic.Uint64
	inlineFailed         atomic.Uint64
	feedbackHelpful      atomic.Uint64
	feedbackHarmful      atomic.Uint64
}

var ReflectionSystemMetrics = &ReflectionMetrics{}

func (m *ReflectionMetrics) RecordRecall(hit bool, lessons, tokens int, shadow bool) {
	m.recallRequests.Add(1)
	if shadow {
		m.shadowRecallRequests.Add(1)
	}
	if hit {
		m.recallHits.Add(1)
	}
	if lessons > 0 {
		m.recalledLessons.Add(uint64(lessons))
	}
	if tokens > 0 {
		m.recalledTokens.Add(uint64(tokens))
	}
}

func (m *ReflectionMetrics) RecordRecallFailure() { m.recallFailures.Add(1) }

func (m *ReflectionMetrics) RecordStored(deduplicated bool) {
	if deduplicated {
		m.deduplicated.Add(1)
		return
	}
	m.stored.Add(1)
}

func (m *ReflectionMetrics) RecordJobEnqueued() { m.jobsEnqueued.Add(1) }

func (m *ReflectionMetrics) RecordJobEnqueueFailure() { m.jobsEnqueueFailed.Add(1) }

func (m *ReflectionMetrics) RecordJobCompleted() { m.jobsCompleted.Add(1) }

func (m *ReflectionMetrics) RecordJobFailure(retry bool) {
	if retry {
		m.jobsRetried.Add(1)
		return
	}
	m.jobsFailed.Add(1)
}

func (m *ReflectionMetrics) RecordHeartbeatFailure() { m.heartbeatFailures.Add(1) }

func (m *ReflectionMetrics) RecordLeaseConflict() { m.leaseConflicts.Add(1) }

func (m *ReflectionMetrics) RecordOutboxPublished() { m.outboxPublished.Add(1) }

func (m *ReflectionMetrics) RecordOutboxPublishFailure() { m.outboxPublishFailed.Add(1) }

func (m *ReflectionMetrics) RecordDLQJob() { m.dlqJobs.Add(1) }

func (m *ReflectionMetrics) RecordRedelivery() { m.messagesRedelivered.Add(1) }

func (m *ReflectionMetrics) RecordProcessingLatency(value time.Duration) {
	if value > 0 {
		m.processingLatencyNS.Add(uint64(value))
		m.processingCount.Add(1)
	}
}

func (m *ReflectionMetrics) RecordPublishLatency(value time.Duration) {
	if value > 0 {
		m.publishLatencyNS.Add(uint64(value))
		m.publishCount.Add(1)
	}
}

func (m *ReflectionMetrics) RecordInlineTriggered() { m.inlineTriggered.Add(1) }

func (m *ReflectionMetrics) RecordInlineCompleted() { m.inlineCompleted.Add(1) }

func (m *ReflectionMetrics) RecordInlineFailed() { m.inlineFailed.Add(1) }

func (m *ReflectionMetrics) RecordFeedback(verdict string) {
	switch verdict {
	case "helpful":
		m.feedbackHelpful.Add(1)
	case "harmful":
		m.feedbackHarmful.Add(1)
	}
}

func (m *ReflectionMetrics) Snapshot() ReflectionMetricSnapshot {
	processingLatencyMS := averageDurationMS(m.processingLatencyNS.Load(), m.processingCount.Load())
	publishLatencyMS := averageDurationMS(m.publishLatencyNS.Load(), m.publishCount.Load())
	return ReflectionMetricSnapshot{
		RecallRequests:       m.recallRequests.Load(),
		RecallHits:           m.recallHits.Load(),
		RecallFailures:       m.recallFailures.Load(),
		ShadowRecallRequests: m.shadowRecallRequests.Load(),
		RecalledLessons:      m.recalledLessons.Load(),
		RecalledTokens:       m.recalledTokens.Load(),
		Stored:               m.stored.Load(),
		Deduplicated:         m.deduplicated.Load(),
		JobsEnqueued:         m.jobsEnqueued.Load(),
		JobsCompleted:        m.jobsCompleted.Load(),
		JobsFailed:           m.jobsFailed.Load(),
		JobsRetried:          m.jobsRetried.Load(),
		JobsEnqueueFailed:    m.jobsEnqueueFailed.Load(),
		HeartbeatFailures:    m.heartbeatFailures.Load(),
		LeaseConflicts:       m.leaseConflicts.Load(),
		OutboxPublished:      m.outboxPublished.Load(),
		OutboxPublishFailed:  m.outboxPublishFailed.Load(),
		DLQJobs:              m.dlqJobs.Load(),
		MessagesRedelivered:  m.messagesRedelivered.Load(),
		ProcessingLatencyMS:  processingLatencyMS,
		PublishLatencyMS:     publishLatencyMS,
		InlineTriggered:      m.inlineTriggered.Load(),
		InlineCompleted:      m.inlineCompleted.Load(),
		InlineFailed:         m.inlineFailed.Load(),
		FeedbackHelpful:      m.feedbackHelpful.Load(),
		FeedbackHarmful:      m.feedbackHarmful.Load(),
	}
}

func averageDurationMS(totalNS, count uint64) uint64 {
	if count == 0 {
		return 0
	}
	return totalNS / count / uint64(time.Millisecond)
}
