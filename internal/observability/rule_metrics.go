package observability

import (
	"strings"
	"sync/atomic"
	"time"
)

type RuleMetricSnapshot struct {
	CompileStarted        uint64 `json:"compile_started"`
	CompileCompleted      uint64 `json:"compile_completed"`
	CompileFailed         uint64 `json:"compile_failed"`
	CompileRetried        uint64 `json:"compile_retried"`
	CompileStale          uint64 `json:"compile_stale"`
	InvalidLLMOutput      uint64 `json:"invalid_llm_output"`
	DAGRejected           uint64 `json:"dag_rejected"`
	Published             uint64 `json:"published"`
	RolledBack            uint64 `json:"rolled_back"`
	MandatoryOverflow     uint64 `json:"mandatory_overflow"`
	SnapshotIntegrityFail uint64 `json:"snapshot_integrity_fail"`
	OptimizerLimited      uint64 `json:"optimizer_limited"`
	ClosureRejected       uint64 `json:"closure_rejected"`
	HookDenied            uint64 `json:"hook_denied"`
	HookApprovalRequired  uint64 `json:"hook_approval_required"`
	PlannerCount          uint64 `json:"planner_count"`
	PlannerTotalNS        uint64 `json:"planner_total_ns"`
	PlannerMaxNS          uint64 `json:"planner_max_ns"`
}

type RuleMetrics struct {
	compileStarted        atomic.Uint64
	compileCompleted      atomic.Uint64
	compileFailed         atomic.Uint64
	compileRetried        atomic.Uint64
	compileStale          atomic.Uint64
	invalidLLMOutput      atomic.Uint64
	dagRejected           atomic.Uint64
	published             atomic.Uint64
	rolledBack            atomic.Uint64
	mandatoryOverflow     atomic.Uint64
	snapshotIntegrityFail atomic.Uint64
	optimizerLimited      atomic.Uint64
	closureRejected       atomic.Uint64
	hookDenied            atomic.Uint64
	hookApprovalRequired  atomic.Uint64
	plannerCount          atomic.Uint64
	plannerTotalNS        atomic.Uint64
	plannerMaxNS          atomic.Uint64
}

var RuleSystemMetrics = &RuleMetrics{}

func (m *RuleMetrics) RecordCompileStarted() { m.compileStarted.Add(1) }

func (m *RuleMetrics) RecordCompileCompleted() { m.compileCompleted.Add(1) }

func (m *RuleMetrics) RecordCompileStale() { m.compileStale.Add(1) }

func (m *RuleMetrics) RecordCompileFailure(err error, retry bool) {
	if retry {
		m.compileRetried.Add(1)
	} else {
		m.compileFailed.Add(1)
	}
	message := ""
	if err != nil {
		message = strings.ToLower(err.Error())
	}
	if strings.Contains(message, "submit_rule_graph") || strings.Contains(message, "rule compiler returned") {
		m.invalidLLMOutput.Add(1)
	}
	if strings.Contains(message, "cycle") || strings.Contains(message, "deterministic dag") {
		m.dagRejected.Add(1)
	}
}

func (m *RuleMetrics) RecordPublished() { m.published.Add(1) }

func (m *RuleMetrics) RecordRollback() { m.rolledBack.Add(1) }

func (m *RuleMetrics) RecordMandatoryOverflow() { m.mandatoryOverflow.Add(1) }

func (m *RuleMetrics) RecordSnapshotIntegrityFailure() { m.snapshotIntegrityFail.Add(1) }

func (m *RuleMetrics) RecordPlanner(duration time.Duration, optimizerLimited bool, closureRejected int) {
	ns := uint64(max(duration.Nanoseconds(), 0))
	m.plannerCount.Add(1)
	m.plannerTotalNS.Add(ns)
	for {
		current := m.plannerMaxNS.Load()
		if ns <= current || m.plannerMaxNS.CompareAndSwap(current, ns) {
			break
		}
	}
	if optimizerLimited {
		m.optimizerLimited.Add(1)
	}
	if closureRejected > 0 {
		m.closureRejected.Add(uint64(closureRejected))
	}
}

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
		CompileStarted:        m.compileStarted.Load(),
		CompileCompleted:      m.compileCompleted.Load(),
		CompileFailed:         m.compileFailed.Load(),
		CompileRetried:        m.compileRetried.Load(),
		CompileStale:          m.compileStale.Load(),
		InvalidLLMOutput:      m.invalidLLMOutput.Load(),
		DAGRejected:           m.dagRejected.Load(),
		Published:             m.published.Load(),
		RolledBack:            m.rolledBack.Load(),
		MandatoryOverflow:     m.mandatoryOverflow.Load(),
		SnapshotIntegrityFail: m.snapshotIntegrityFail.Load(),
		OptimizerLimited:      m.optimizerLimited.Load(),
		ClosureRejected:       m.closureRejected.Load(),
		HookDenied:            m.hookDenied.Load(),
		HookApprovalRequired:  m.hookApprovalRequired.Load(),
		PlannerCount:          m.plannerCount.Load(),
		PlannerTotalNS:        m.plannerTotalNS.Load(),
		PlannerMaxNS:          m.plannerMaxNS.Load(),
	}
}
