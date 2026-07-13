package observability

import (
	"errors"
	"testing"
	"time"
)

func TestRuleMetricsRecordsAlertableSignals(t *testing.T) {
	metrics := &RuleMetrics{}
	metrics.RecordCompileStarted()
	metrics.RecordCompileFailure(errors.New("invalid submit_rule_graph payload"), true)
	metrics.RecordPlanner(2*time.Millisecond, true, 3)
	metrics.RecordHookDecision("denied")
	metrics.RecordMandatoryOverflow()
	snapshot := metrics.Snapshot()
	if snapshot.CompileStarted != 1 || snapshot.CompileRetried != 1 || snapshot.InvalidLLMOutput != 1 || snapshot.OptimizerLimited != 1 || snapshot.ClosureRejected != 3 || snapshot.HookDenied != 1 || snapshot.MandatoryOverflow != 1 {
		t.Fatalf("unexpected rule metrics snapshot: %+v", snapshot)
	}
}
