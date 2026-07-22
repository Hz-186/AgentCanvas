package observability

import "sync/atomic"

type MemoryMetrics struct {
	workingReadFailures  atomic.Int64
	workingWriteFailures atomic.Int64
	dreamScheduled       atomic.Int64
	dreamFailures        atomic.Int64
}

var MemoryRuntimeMetrics = &MemoryMetrics{}

func (m *MemoryMetrics) RecordWorkingReadFailure()  { m.workingReadFailures.Add(1) }
func (m *MemoryMetrics) RecordWorkingWriteFailure() { m.workingWriteFailures.Add(1) }
func (m *MemoryMetrics) RecordDreamScheduled()      { m.dreamScheduled.Add(1) }
func (m *MemoryMetrics) RecordDreamFailure()        { m.dreamFailures.Add(1) }

func (m *MemoryMetrics) Snapshot() map[string]int64 {
	return map[string]int64{
		"working_memory_read_failures":  m.workingReadFailures.Load(),
		"working_memory_write_failures": m.workingWriteFailures.Load(),
		"dream_scheduled":               m.dreamScheduled.Load(),
		"dream_failures":                m.dreamFailures.Load(),
	}
}
