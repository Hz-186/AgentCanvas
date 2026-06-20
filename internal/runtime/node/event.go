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
	if event.NodeID == "" {
		event.NodeID = rc.CurrentNodeID
	}
	if event.NodeType == "" {
		event.NodeType = rc.CurrentNodeType
	}
	_ = rc.Events.Emit(ctx, event)
}
