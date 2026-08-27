package observability

import (
	"sync/atomic"
)

// ReflectionMetricSnapshot covers only the runtime inline reflection signal.
// The historical reflection subsystem (jobs, queue, recall, feedback) was
// retired; terminal reflection now flows through the canonical memory write
// pipeline, which is instrumented by MemoryRuntimeMetrics instead.
type ReflectionMetricSnapshot struct {
	InlineTriggered uint64 `json:"inline_triggered"`
	InlineCompleted uint64 `json:"inline_completed"`
	InlineFailed    uint64 `json:"inline_failed"`
}

type ReflectionMetrics struct {
	inlineTriggered atomic.Uint64
	inlineCompleted atomic.Uint64
	inlineFailed    atomic.Uint64
}

var ReflectionSystemMetrics = &ReflectionMetrics{}

func (m *ReflectionMetrics) RecordInlineTriggered() { m.inlineTriggered.Add(1) }

func (m *ReflectionMetrics) RecordInlineCompleted() { m.inlineCompleted.Add(1) }

func (m *ReflectionMetrics) RecordInlineFailed() { m.inlineFailed.Add(1) }

func (m *ReflectionMetrics) Snapshot() ReflectionMetricSnapshot {
	return ReflectionMetricSnapshot{
		InlineTriggered: m.inlineTriggered.Load(),
		InlineCompleted: m.inlineCompleted.Load(),
		InlineFailed:    m.inlineFailed.Load(),
	}
}
