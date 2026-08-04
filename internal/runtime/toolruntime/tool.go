package toolruntime

import (
	"context"
	"encoding/json"
)

type ToolRunContext struct {
	OwnerID         int64
	AgentID         int64
	AgentReleaseID  int64
	RunID           int64
	DelegationDepth int
	ConversationID  *int64
	// Task is the current run objective. Tools use it as a semantic query
	// when the model omits an explicit query (for example memory recall).
	Task string
}

type ToolResult struct {
	ContentJSON json.RawMessage `json:"content_json,omitempty"`
	ContentText string          `json:"content_text,omitempty"`
	IsError     bool            `json:"is_error,omitempty"`
	Metadata    map[string]any  `json:"metadata,omitempty"`
	Approval    *ToolApproval   `json:"approval,omitempty"`
}

// ToolApproval is a structured, user-facing approval request emitted by a
// tool after it has inspected its arguments. Unlike risk approvals, these
// requests can expose mutually exclusive choices (for example resolving a
// conflicting memory as keep-existing, replace, or keep-both).
type ToolApproval struct {
	Kind    string           `json:"kind"`
	Title   string           `json:"title"`
	Reason  string           `json:"reason"`
	Options []ApprovalOption `json:"options,omitempty"`
}

type ApprovalOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
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

const (
	ExecutionSerial     = "serial"
	ExecutionDelegation = "delegation"
)

type ToolMetadata struct {
	RiskLevel        string   `json:"risk_level"`
	RequiresApproval bool     `json:"requires_approval"`
	TimeoutMS        int      `json:"timeout_ms,omitempty"`
	MaxOutputBytes   int      `json:"max_output_bytes,omitempty"`
	SideEffect       string   `json:"side_effect,omitempty"`
	AllowedHosts     []string `json:"allowed_hosts,omitempty"`
	ExecutionClass   string   `json:"execution_class,omitempty"`
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
		return ToolMetadata{
			RiskLevel:  RiskLow,
			SideEffect: SideEffectNone,
		}
	}
	if provider, ok := tool.(MetadataProvider); ok {
		metadata := provider.Metadata()
		if metadata.RiskLevel == "" {
			metadata.RiskLevel = RiskLow
		}
		if metadata.SideEffect == "" {
			metadata.SideEffect = SideEffectNone
		}
		if metadata.ExecutionClass == "" {
			metadata.ExecutionClass = ExecutionSerial
		}
		return metadata
	}
	return ToolMetadata{
		RiskLevel:      RiskLow,
		SideEffect:     SideEffectNone,
		ExecutionClass: ExecutionSerial,
	}
}

type Registry interface {
	LoadForAgent(ctx context.Context, ownerID int64, toolIDs []int64) ([]RuntimeTool, error)
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
