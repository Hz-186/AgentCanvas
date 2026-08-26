package agent

import (
	"encoding/json"
	"time"

	"agentcanvas/internal/domain"
	"agentcanvas/internal/runtime/toolruntime"
)

const (
	ApprovalStatusPending  = "pending"
	ApprovalStatusApproved = "approved"
	ApprovalStatusRejected = "rejected"
)

type ApprovalRequest struct {
	domain.BaseModel
	RunID         int64                           `json:"run_id" gorm:"column:run_id"`
	ToolCallID    string                          `json:"tool_call_id" gorm:"column:tool_call_id"`
	InteractionID string                          `json:"interaction_id" gorm:"column:interaction_id"`
	ToolName      string                          `json:"tool_name" gorm:"column:tool_name"`
	RiskLevel     string                          `json:"risk_level" gorm:"column:risk_level"`
	Reason        string                          `json:"reason" gorm:"column:reason"`
	RequestJSON   json.RawMessage                 `json:"request_json" gorm:"column:request_json"`
	Options       json.RawMessage                 `json:"options,omitempty" gorm:"-"`
	Questions     []toolruntime.UserInputQuestion `json:"questions,omitempty" gorm:"-"`
	Status        string                          `json:"status" gorm:"column:status"`
	DecisionNote  string                          `json:"decision_note" gorm:"column:decision_note"`
	IsBlocking    bool                            `json:"is_blocking,omitempty" gorm:"-"`
	DecidedAt     *time.Time                      `json:"decided_at,omitempty" gorm:"column:decided_at"`
}

func (ApprovalRequest) TableName() string { return "agent_approval_requests" }

type RunCheckpoint struct {
	domain.ImmutableModel
	RunID          int64           `json:"run_id" gorm:"column:run_id"`
	CheckpointJSON json.RawMessage `json:"checkpoint_json" gorm:"column:checkpoint_json"`
}

func (RunCheckpoint) TableName() string { return "agent_run_checkpoints" }
