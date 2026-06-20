package engine

import (
	"context"

	runtimeevent "agentcanvas/internal/runtime/event"
)

type EventEmitter interface {
	Emit(ctx context.Context, event runtimeevent.Event) error
}

type RunContext struct {
	OwnerID         int64
	AgentID         int64
	FlowVersionID   int64
	RunID           int64
	ConversationID  *int64
	Input           map[string]any
	Variables       map[string]any
	NodeInputs      map[string]NodeInput
	NodeOutputs     map[string]NodeOutput
	NodeErrors      map[string]string
	NodeLatencies   map[string]int
	ExecutedNodes   map[string]bool
	Events          EventEmitter
	CurrentNodeID   string
	CurrentNodeType string
}

type NodeInput map[string]any
type NodeOutput map[string]any
