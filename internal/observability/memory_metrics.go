package observability

import "sync/atomic"

type MemoryMetrics struct {
	dreamScheduled          atomic.Int64
	dreamFailures           atomic.Int64
	dreamLLMCalls           atomic.Int64
	dreamLLMLatencyMS       atomic.Int64
	dreamInputMessages      atomic.Int64
	candidateWrites         atomic.Int64
	candidateWriteLatencyMS atomic.Int64
	memoryApprovalDecisions atomic.Int64
	memoryApprovalWaitMS    atomic.Int64
	schedulerRuns           atomic.Int64
	schedulerFailures       atomic.Int64
	schedulerLockFailures   atomic.Int64
}

var MemoryRuntimeMetrics = &MemoryMetrics{}

func (m *MemoryMetrics) RecordDreamScheduled() { m.dreamScheduled.Add(1) }
func (m *MemoryMetrics) RecordDreamFailure()   { m.dreamFailures.Add(1) }
func (m *MemoryMetrics) RecordDreamLLM(messageCount int, latencyMS int64) {
	m.dreamLLMCalls.Add(1)
	m.dreamInputMessages.Add(int64(messageCount))
	m.dreamLLMLatencyMS.Add(latencyMS)
}
func (m *MemoryMetrics) RecordCandidateWrite(latencyMS int64) {
	m.candidateWrites.Add(1)
	m.candidateWriteLatencyMS.Add(latencyMS)
}
func (m *MemoryMetrics) RecordMemoryApprovalWait(latencyMS int64) {
	m.memoryApprovalDecisions.Add(1)
	if latencyMS > 0 {
		m.memoryApprovalWaitMS.Add(latencyMS)
	}
}
func (m *MemoryMetrics) Snapshot() map[string]int64 {
	return map[string]int64{
		"dream_scheduled":                   m.dreamScheduled.Load(),
		"dream_failures":                    m.dreamFailures.Load(),
		"dream_llm_calls":                   m.dreamLLMCalls.Load(),
		"dream_llm_latency_ms":              m.dreamLLMLatencyMS.Load(),
		"dream_input_messages":              m.dreamInputMessages.Load(),
		"memory_candidate_writes":           m.candidateWrites.Load(),
		"memory_candidate_write_latency_ms": m.candidateWriteLatencyMS.Load(),
		"memory_approval_decisions":         m.memoryApprovalDecisions.Load(),
		"memory_approval_wait_ms":           m.memoryApprovalWaitMS.Load(),
		"scheduler_runs":                    m.schedulerRuns.Load(),
		"scheduler_failures":                m.schedulerFailures.Load(),
		"scheduler_lock_failures":           m.schedulerLockFailures.Load(),
	}
}
