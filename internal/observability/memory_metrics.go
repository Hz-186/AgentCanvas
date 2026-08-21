package observability

import "sync/atomic"

type MemoryMetrics struct {
	workingReadFailures   atomic.Int64
	workingWriteFailures  atomic.Int64
	dreamScheduled        atomic.Int64
	dreamFailures         atomic.Int64
	schedulerRuns         atomic.Int64
	schedulerFailures     atomic.Int64
	schedulerLockFailures atomic.Int64
}

var MemoryRuntimeMetrics = &MemoryMetrics{}

func (m *MemoryMetrics) RecordWorkingReadFailure()   { m.workingReadFailures.Add(1) }
func (m *MemoryMetrics) RecordWorkingWriteFailure()  { m.workingWriteFailures.Add(1) }
func (m *MemoryMetrics) RecordDreamScheduled()       { m.dreamScheduled.Add(1) }
func (m *MemoryMetrics) RecordDreamFailure()         { m.dreamFailures.Add(1) }
func (m *MemoryMetrics) RecordSchedulerRun()         { m.schedulerRuns.Add(1) }
func (m *MemoryMetrics) RecordSchedulerFailure()     { m.schedulerFailures.Add(1) }
func (m *MemoryMetrics) RecordSchedulerLockFailure() { m.schedulerLockFailures.Add(1) }

func (m *MemoryMetrics) Snapshot() map[string]int64 {
	return map[string]int64{
		"working_memory_read_failures":  m.workingReadFailures.Load(),
		"working_memory_write_failures": m.workingWriteFailures.Load(),
		"dream_scheduled":               m.dreamScheduled.Load(),
		"dream_failures":                m.dreamFailures.Load(),
		"scheduler_runs":                m.schedulerRuns.Load(),
		"scheduler_failures":            m.schedulerFailures.Load(),
		"scheduler_lock_failures":       m.schedulerLockFailures.Load(),
	}
}
