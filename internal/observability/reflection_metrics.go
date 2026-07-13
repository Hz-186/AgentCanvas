package observability

import "sync/atomic"

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

func (m *ReflectionMetrics) RecordJobCompleted() { m.jobsCompleted.Add(1) }

func (m *ReflectionMetrics) RecordJobFailure(retry bool) {
	if retry {
		m.jobsRetried.Add(1)
		return
	}
	m.jobsFailed.Add(1)
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
		InlineTriggered:      m.inlineTriggered.Load(),
		InlineCompleted:      m.inlineCompleted.Load(),
		InlineFailed:         m.inlineFailed.Load(),
		FeedbackHelpful:      m.feedbackHelpful.Load(),
		FeedbackHarmful:      m.feedbackHarmful.Load(),
	}
}
