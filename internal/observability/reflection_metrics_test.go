package observability

import (
	"testing"
)

func TestReflectionMetricsRecordsInlineLifecycle(t *testing.T) {
	metrics := &ReflectionMetrics{}
	metrics.RecordInlineTriggered()
	metrics.RecordInlineTriggered()
	metrics.RecordInlineCompleted()
	metrics.RecordInlineFailed()

	snapshot := metrics.Snapshot()
	if snapshot.InlineTriggered != 2 || snapshot.InlineCompleted != 1 || snapshot.InlineFailed != 1 {
		t.Fatalf("unexpected inline reflection snapshot: %+v", snapshot)
	}
}
