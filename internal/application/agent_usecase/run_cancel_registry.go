package agent_usecase

import (
	"context"
	"sync"
)

type runCancelRegistry struct {
	mu      sync.Mutex
	cancels map[int64]context.CancelFunc
}

func newRunCancelRegistry() *runCancelRegistry {
	return &runCancelRegistry{cancels: make(map[int64]context.CancelFunc)}
}

func (r *runCancelRegistry) Register(runID int64, cancel context.CancelFunc) {
	if r == nil || runID <= 0 || cancel == nil {
		return
	}
	r.mu.Lock()
	r.cancels[runID] = cancel
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
	if r == nil || runID <= 0 {
		return false
	}
	r.mu.Lock()
	cancel, ok := r.cancels[runID]
	r.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}
