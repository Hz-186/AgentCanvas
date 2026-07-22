package workflow_usecase

import (
	"context"
	"sync"

	runtimeagent "agentcanvas/internal/runtime/agent"
)

type runCancelRegistry struct {
	mu      sync.Mutex
	cancels map[int64]runCancelEntry
}

type runCancelReason string

const (
	runCancelReasonCancel runCancelReason = "cancel"
	runCancelReasonPause  runCancelReason = "pause"
)

type runCancelEntry struct {
	cancel context.CancelCauseFunc
	reason runCancelReason
}

func newRunCancelRegistry() *runCancelRegistry {
	return &runCancelRegistry{cancels: make(map[int64]runCancelEntry)}
}

func (r *runCancelRegistry) Register(runID int64, cancel context.CancelCauseFunc) {
	if r == nil || runID <= 0 || cancel == nil {
		return
	}
	r.mu.Lock()
	r.cancels[runID] = runCancelEntry{cancel: cancel}
	r.mu.Unlock()
}

func (r *runCancelRegistry) Unregister(runID int64) {
	if r == nil || runID <= 0 {
		return
	}
	r.mu.Lock()
	delete(r.cancels, runID)
	r.mu.Unlock()
}

func (r *runCancelRegistry) Cancel(runID int64) bool {
	return r.cancelWithReason(runID, runCancelReasonCancel)
}

func (r *runCancelRegistry) Pause(runID int64) bool {
	return r.cancelWithReason(runID, runCancelReasonPause)
}

func (r *runCancelRegistry) Reason(runID int64) runCancelReason {
	if r == nil || runID <= 0 {
		return ""
	}
	r.mu.Lock()
	entry := r.cancels[runID]
	r.mu.Unlock()
	return entry.reason
}

func (r *runCancelRegistry) cancelWithReason(runID int64, reason runCancelReason) bool {
	if r == nil || runID <= 0 {
		return false
	}
	r.mu.Lock()
	entry, ok := r.cancels[runID]
	if ok {
		entry.reason = reason
		r.cancels[runID] = entry
	}
	r.mu.Unlock()
	if ok && entry.cancel != nil {
		cause := context.Canceled
		if reason == runCancelReasonPause {
			cause = runtimeagent.ErrRunPaused
		}
		entry.cancel(cause)
	}
	return ok
}
