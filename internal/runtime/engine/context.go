package engine

import (
	"context"
	"encoding/json"

	runtimeevent "agentcanvas/internal/runtime/event"
)

type EventEmitter interface {
	Emit(ctx context.Context, event runtimeevent.Event) error
}

type AgentStepRecord struct {
	NodeID        string
	StepIndex     int
	StepType      string
	Role          string
	Content       string
	ToolCallID    string
	ToolName      string
	ArgumentsJSON json.RawMessage
	OutputJSON    json.RawMessage
	ErrorMessage  string
	TokenCount    int
	LatencyMS     int
	ProviderID    int64
	Model         string
}

type AgentStepRecorder interface {
	RecordAgentStep(ctx context.Context, rc *RunContext, step AgentStepRecord) error
}

type RunContext struct {
	OwnerID           int64                 `json:"owner_id" tag:"multi-tenant user ID"`
	WorkflowID        int64                 `json:"workflow_id" tag:"parent agent ID"`
	FlowVersionID     int64                 `json:"flow_version_id" tag:"DSL version ID"`
	RunID             int64                 `json:"run_id" tag:"unique run ID"`
	ParentRunID       *int64                `json:"parent_run_id" tag:"optional parent run ID"`
	CallDepth         int                   `json:"call_depth" tag:"nested workflow call depth"`
	WorkflowCallChain []int64               `json:"workflow_call_chain" tag:"ancestor agent IDs including current agent"`
	ConversationID    *int64                `json:"conversation_id" tag:"optional conversation ID"`
	Input             map[string]any        `json:"input" tag:"original user input"`
	Variables         map[string]any        `json:"variables" tag:"user-defined global vars"`
	NodeInputs        map[string]NodeInput  `json:"node_inputs" tag:"per-node inputs"`
	NodeOutputs       map[string]NodeOutput `json:"node_outputs" tag:"per-node outputs"`
	NodeErrors        map[string]string     `json:"node_errors" tag:"per-node error messages"`
	NodeLatencies     map[string]int        `json:"node_latencies" tag:"per-node latency in ms"`
	ExecutedNodes     map[string]bool       `json:"executed_nodes" tag:"per-node execution flags"`
	Events            EventEmitter          `json:"events" tag:"event emitter"`
	AgentSteps        AgentStepRecorder     `json:"agent_steps" tag:"agent step recorder"`
	CurrentNodeID     string                `json:"current_node_id" tag:"current node ID"`
	CurrentNodeType   string                `json:"current_node_type" tag:"current node type"`
}

type NodeInput map[string]any
type NodeOutput map[string]any
