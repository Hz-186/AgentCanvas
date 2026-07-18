package observability

import "testing"

func TestRuleMetricsRecordsRuntimeSignals(t *testing.T) {
	metrics := &RuleMetrics{}
	metrics.RecordPublished()
	metrics.RecordRollback()
	metrics.RecordHookDecision("denied")
	metrics.RecordMandatoryOverflow()
	metrics.RecordSnapshotIntegrityFailure()
	snapshot := metrics.Snapshot()
	if snapshot.Published != 1 || snapshot.RolledBack != 1 || snapshot.HookDenied != 1 || snapshot.MandatoryOverflow != 1 || snapshot.SnapshotIntegrityFail != 1 {
		t.Fatalf("unexpected rule metrics snapshot: %+v", snapshot)
	}
}
