package toolruntime

import (
	"context"
	"encoding/json"
)

type ToolRunContext struct {
	OwnerID           int64
	WorkflowID        int64
	RunID             int64
	NodeID            string
	CallDepth         int
	WorkflowCallChain []int64
	ConversationID    *int64
}

type ToolResult struct {
	ContentJSON json.RawMessage `json:"content_json,omitempty"`
	ContentText string          `json:"content_text,omitempty"`
	IsError     bool            `json:"is_error,omitempty"`
	Metadata    map[string]any  `json:"metadata,omitempty"`
}

const (
	RiskLow    = "low"
	RiskMedium = "medium"
	RiskHigh   = "high"
)

const (
	SideEffectNone           = "none"
	SideEffectRead           = "read"
	SideEffectWrite          = "write"
	SideEffectExternalAction = "external_action"
)

type ToolMetadata struct {
	RiskLevel        string   `json:"risk_level"`
	RequiresApproval bool     `json:"requires_approval"`
	TimeoutMS        int      `json:"timeout_ms,omitempty"`
	MaxOutputBytes   int      `json:"max_output_bytes,omitempty"`
	SideEffect       string   `json:"side_effect,omitempty"`
	AllowedHosts     []string `json:"allowed_hosts,omitempty"`
}

type RuntimeTool interface {
	Name() string
	Description() string
	Parameters() json.RawMessage
	Execute(ctx context.Context, rc ToolRunContext, input json.RawMessage) (*ToolResult, error)
}

type MetadataProvider interface {
	Metadata() ToolMetadata
}

func MetadataOf(tool RuntimeTool) ToolMetadata {
	if tool == nil {
		return ToolMetadata{RiskLevel: RiskLow, SideEffect: SideEffectNone}
	}
	if provider, ok := tool.(MetadataProvider); ok {
		metadata := provider.Metadata()
		if metadata.RiskLevel == "" {
			metadata.RiskLevel = RiskLow
		}
		if metadata.SideEffect == "" {
			metadata.SideEffect = SideEffectNone
		}
		return metadata
	}
	return ToolMetadata{RiskLevel: RiskLow, SideEffect: SideEffectNone}
}

type Registry interface {
	LoadForAgent(ctx context.Context, ownerID int64, toolIDs []int64) ([]RuntimeTool, error)
}

type WorkflowCallRequest struct {
	OwnerID           int64          `json:"owner_id"`
	ParentRunID       int64          `json:"parent_run_id"`
	CallerWorkflowID  int64          `json:"caller_workflow_id"`
	CallerNodeID      string         `json:"caller_node_id"`
	WorkflowID        int64          `json:"workflow_id"`
	FlowVersionID     int64          `json:"flow_version_id"`
	Input             map[string]any `json:"input"`
	CallDepth         int            `json:"call_depth"`
	WorkflowCallChain []int64        `json:"workflow_call_chain"`
	MaxDepth          int            `json:"max_depth"`
}

type WorkflowCallResult struct {
	RunID         int64          `json:"run_id"`
	WorkflowID    int64          `json:"workflow_id"`
	FlowVersionID int64          `json:"flow_version_id"`
	Status        string         `json:"status"`
	Output        map[string]any `json:"output"`
	Error         string         `json:"error,omitempty"`
	LatencyMS     int            `json:"latency_ms"`
}

type WorkflowCaller interface {
	CallWorkflow(ctx context.Context, req WorkflowCallRequest) (*WorkflowCallResult, error)
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
