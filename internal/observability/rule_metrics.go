package observability

import "sync/atomic"

type RuleMetricSnapshot struct {
	Published             uint64 `json:"published"`
	RolledBack            uint64 `json:"rolled_back"`
	MandatoryOverflow     uint64 `json:"mandatory_overflow"`
	SnapshotIntegrityFail uint64 `json:"snapshot_integrity_fail"`
	HookDenied            uint64 `json:"hook_denied"`
	HookApprovalRequired  uint64 `json:"hook_approval_required"`
}

type RuleMetrics struct {
	published             atomic.Uint64
	rolledBack            atomic.Uint64
	mandatoryOverflow     atomic.Uint64
	snapshotIntegrityFail atomic.Uint64
	hookDenied            atomic.Uint64
	hookApprovalRequired  atomic.Uint64
}

var RuleSystemMetrics = &RuleMetrics{}

func (m *RuleMetrics) RecordPublished() { m.published.Add(1) }

func (m *RuleMetrics) RecordRollback() { m.rolledBack.Add(1) }

func (m *RuleMetrics) RecordMandatoryOverflow() { m.mandatoryOverflow.Add(1) }

func (m *RuleMetrics) RecordSnapshotIntegrityFailure() { m.snapshotIntegrityFail.Add(1) }

func (m *RuleMetrics) RecordHookDecision(decision string) {
	switch decision {
	case "denied":
		m.hookDenied.Add(1)
	case "approval_required":
		m.hookApprovalRequired.Add(1)
	}
}

func (m *RuleMetrics) Snapshot() RuleMetricSnapshot {
	return RuleMetricSnapshot{
		Published:             m.published.Load(),
		RolledBack:            m.rolledBack.Load(),
		MandatoryOverflow:     m.mandatoryOverflow.Load(),
		SnapshotIntegrityFail: m.snapshotIntegrityFail.Load(),
		HookDenied:            m.hookDenied.Load(),
		HookApprovalRequired:  m.hookApprovalRequired.Load(),
	}
}
