package workflow

import (
	"encoding/json"
	"time"
)

const (
	ApprovalStatusPending  = "pending"
	ApprovalStatusApproved = "approved"
	ApprovalStatusRejected = "rejected"
)

type ApprovalRequest struct {
	ID            int64           `json:"id" gorm:"primaryKey;column:id"`
	OwnerID       int64           `json:"owner_id" gorm:"column:owner_id"`
	WorkflowID    int64           `json:"workflow_id" gorm:"column:workflow_id"`
	RunID         int64           `json:"run_id" gorm:"column:run_id"`
	NodeID        string          `json:"node_id" gorm:"column:node_id"`
	ToolCallID    string          `json:"tool_call_id" gorm:"column:tool_call_id"`
	InteractionID string          `json:"interaction_id" gorm:"column:interaction_id"`
	ToolName      string          `json:"tool_name" gorm:"column:tool_name"`
	RiskLevel     string          `json:"risk_level" gorm:"column:risk_level"`
	Reason        string          `json:"reason" gorm:"column:reason"`
	RequestJSON   json.RawMessage `json:"request_json" gorm:"column:request_json"`
	Options       json.RawMessage `json:"options,omitempty" gorm:"-"`
	Status        string          `json:"status" gorm:"column:status"`
	DecisionNote  string          `json:"decision_note" gorm:"column:decision_note"`
	DecidedAt     *time.Time      `json:"decided_at" gorm:"column:decided_at"`
	CreatedAt     time.Time       `json:"created_at" gorm:"column:created_at"`
	UpdatedAt     time.Time       `json:"updated_at" gorm:"column:updated_at"`
}

func (ApprovalRequest) TableName() string { return "approval_requests" }

type WorkflowCheckpoint struct {
	ID                    int64           `json:"id" gorm:"primaryKey;column:id"`
	OwnerID               int64           `json:"owner_id" gorm:"column:owner_id"`
	WorkflowID            int64           `json:"workflow_id" gorm:"column:workflow_id"`
	RunID                 int64           `json:"run_id" gorm:"column:run_id"`
	NodeID                string          `json:"node_id" gorm:"column:node_id"`
	Status                string          `json:"status" gorm:"column:status"`
	SnapshotVersion       int             `json:"snapshot_version" gorm:"column:snapshot_version"`
	InteractionID         string          `json:"interaction_id" gorm:"column:interaction_id"`
	RuntimeCheckpointJSON json.RawMessage `json:"runtime_checkpoint_json" gorm:"column:runtime_checkpoint_json"`
	MessagesJSON          json.RawMessage `json:"messages_json" gorm:"column:messages_json"`
	MessagesSummary       string          `json:"messages_summary" gorm:"column:messages_summary"`
	StepsJSON             json.RawMessage `json:"steps_json" gorm:"column:steps_json"`
	PendingToolCallJSON   json.RawMessage `json:"pending_tool_call_json" gorm:"column:pending_tool_call_json"`
	ContextJSON           json.RawMessage `json:"context_json" gorm:"column:context_json"`
	ToolRegistryHash      string          `json:"tool_registry_hash" gorm:"column:tool_registry_hash"`
	ToolPolicyHash        string          `json:"tool_policy_hash" gorm:"column:tool_policy_hash"`
	CreatedAt             time.Time       `json:"created_at" gorm:"column:created_at"`
	UpdatedAt             time.Time       `json:"updated_at" gorm:"column:updated_at"`
}

func (WorkflowCheckpoint) TableName() string { return "workflow_checkpoints" }
