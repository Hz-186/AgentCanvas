package engine

import (
	"context"

	runtimeevent "agentcanvas/internal/runtime/event"
)

type EventEmitter interface {
	Emit(ctx context.Context, event runtimeevent.Event) error
}

type RunContext struct {
	OwnerID         int64                 `json:"owner_id" tag:"multi-tenant user ID"`
	AgentID         int64                 `json:"agent_id" tag:"parent agent ID"`
	FlowVersionID   int64                 `json:"flow_version_id" tag:"DSL version ID"`
	RunID           int64                 `json:"run_id" tag:"unique run ID"`
	ConversationID  *int64                `json:"conversation_id" tag:"optional conversation ID"`
	Input           map[string]any        `json:"input" tag:"original user input"`
	Variables       map[string]any        `json:"variables" tag:"user-defined global vars"`
	NodeInputs      map[string]NodeInput  `json:"node_inputs" tag:"per-node inputs"`
	NodeOutputs     map[string]NodeOutput `json:"node_outputs" tag:"per-node outputs"`
	NodeErrors      map[string]string     `json:"node_errors" tag:"per-node error messages"`
	NodeLatencies   map[string]int        `json:"node_latencies" tag:"per-node latency in ms"`
	ExecutedNodes   map[string]bool       `json:"executed_nodes" tag:"per-node execution flags"`
	Events          EventEmitter          `json:"events" tag:"event emitter"`
	CurrentNodeID   string                `json:"current_node_id" tag:"current node ID"`
	CurrentNodeType string                `json:"current_node_type" tag:"current node type"`
}

type NodeInput map[string]any
type NodeOutput map[string]any
