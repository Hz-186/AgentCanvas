package agentruntime

import (
	"context"
	"encoding/json"

	runtimeevent "agentcanvas/internal/runtime/event"
	"agentcanvas/internal/runtime/harness/rules"
)

type EventEmitter interface {
	Emit(ctx context.Context, event runtimeevent.Event) error
}

type AgentStepRecord struct {
	StepIndex     int
	StepType      string
	Role          string
	Content       string
	ToolCallID    string
	ToolName      string
	ArgumentsJSON json.RawMessage
	OutputJSON    json.RawMessage
	Compressed    bool
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
	OwnerID         int64             `json:"owner_id" tag:"multi-tenant user ID"`
	AgentID         int64             `json:"agent_id,omitempty" tag:"Agent ID"`
	AgentReleaseID  int64             `json:"agent_release_id,omitempty" tag:"pinned Agent release ID"`
	RuleHash        string            `json:"rule_hash,omitempty" tag:"pinned Agent release rule hash"`
	Rules           []rules.Rule      `json:"-" tag:"verified immutable rules"`
	RunID           int64             `json:"run_id" tag:"unique run ID"`
	ParentRunID     *int64            `json:"parent_run_id" tag:"optional parent run ID"`
	DelegationDepth int               `json:"delegation_depth" tag:"nested subagent depth"`
	ConversationID  *int64            `json:"conversation_id" tag:"optional conversation ID"`
	Input           map[string]any    `json:"input" tag:"original user input"`
	Events          EventEmitter      `json:"events" tag:"event emitter"`
	AgentSteps      AgentStepRecorder `json:"agent_steps" tag:"agent step recorder"`
}

type RunInput map[string]any
type RunOutput map[string]any
