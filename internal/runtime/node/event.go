package node

import (
	"context"

	"agentcanvas/internal/runtime/engine"
	runtimeevent "agentcanvas/internal/runtime/event"
)

func emitRuntimeEvent(ctx context.Context, rc *engine.RunContext, event runtimeevent.Event) {
	if rc == nil || rc.Events == nil {
		return
	}
	_ = rc.Events.Emit(ctx, event)
}
