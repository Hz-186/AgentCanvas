package engine

import (
	"context"

	runtimeevent "agentcanvas/internal/runtime/event"
)

type EventEmitter interface {
	Emit(ctx context.Context, event runtimeevent.Event) error
}

type RunContext struct {
	OwnerID        int64
	AgentID        int64
	FlowVersionID  int64
	RunID          int64
	ConversationID *int64
	Input          map[string]any
	Variables      map[string]any
	NodeOutputs    map[string]NodeOutput
	Events         EventEmitter
}

type NodeInput map[string]any
type NodeOutput map[string]any
