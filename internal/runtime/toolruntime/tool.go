package toolruntime

import (
	"context"
	"encoding/json"
)

type ToolRunContext struct {
	OwnerID   int64
	AgentID   int64
	RunID     int64
	NodeID    string
	CallDepth int
}

type ToolResult struct {
	ContentJSON json.RawMessage `json:"content_json,omitempty"`
	ContentText string          `json:"content_text,omitempty"`
	IsError     bool            `json:"is_error,omitempty"`
	Metadata    map[string]any  `json:"metadata,omitempty"`
}

type RuntimeTool interface {
	Name() string
	Description() string
	Parameters() json.RawMessage
	Execute(ctx context.Context, rc ToolRunContext, input json.RawMessage) (*ToolResult, error)
}

type Registry interface {
	LoadForAgent(ctx context.Context, ownerID int64, toolIDs []int64) ([]RuntimeTool, error)
}

type AgentCallRequest struct {
	OwnerID       int64          `json:"owner_id"`
	ParentRunID   int64          `json:"parent_run_id"`
	CallerAgentID int64          `json:"caller_agent_id"`
	CallerNodeID  string         `json:"caller_node_id"`
	AgentID       int64          `json:"agent_id"`
	FlowVersionID int64          `json:"flow_version_id"`
	Input         map[string]any `json:"input"`
	CallDepth     int            `json:"call_depth"`
	MaxDepth      int            `json:"max_depth"`
}

type AgentCallResult struct {
	RunID         int64          `json:"run_id"`
	AgentID       int64          `json:"agent_id"`
	FlowVersionID int64          `json:"flow_version_id"`
	Status        string         `json:"status"`
	Output        map[string]any `json:"output"`
	Error         string         `json:"error,omitempty"`
	LatencyMS     int            `json:"latency_ms"`
}

type AgentCaller interface {
	CallAgent(ctx context.Context, req AgentCallRequest) (*AgentCallResult, error)
}

func ResultFromValue(value any) (*ToolResult, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return &ToolResult{
		ContentJSON: data,
		ContentText: string(data),
	}, nil
}
