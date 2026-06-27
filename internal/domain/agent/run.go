package agent

import (
	"encoding/json"
	"time"
)

const (
	RunStatusRunning   = "running"
	RunStatusSucceeded = "succeeded"
	RunStatusFailed    = "failed"
	RunStatusCancelled = "cancelled"
)

type Run struct {
	ID             int64           `json:"id" gorm:"primaryKey;column:id"`
	OwnerID        int64           `json:"owner_id" gorm:"column:owner_id"`
	AgentID        int64           `json:"agent_id" gorm:"column:agent_id"`
	FlowVersionID  int64           `json:"flow_version_id" gorm:"column:flow_version_id"`
	ConversationID *int64          `json:"conversation_id" gorm:"column:conversation_id"`
	ParentRunID    *int64          `json:"parent_run_id" gorm:"column:parent_run_id"`
	CallerNodeID   string          `json:"caller_node_id" gorm:"column:caller_node_id"`
	CallDepth      int             `json:"call_depth" gorm:"column:call_depth"`
	Status         string          `json:"status" gorm:"column:status"`
	InputJSON      json.RawMessage `json:"input_json" gorm:"column:input_json"`
	OutputJSON     json.RawMessage `json:"output_json" gorm:"column:output_json"`
	ErrorMessage   string          `json:"error_message" gorm:"column:error_message"`
	TotalTokens    int             `json:"total_tokens" gorm:"column:total_tokens"`
	LatencyMS      int             `json:"latency_ms" gorm:"column:latency_ms"`
	StartedAt      time.Time       `json:"started_at" gorm:"column:started_at"`
	FinishedAt     *time.Time      `json:"finished_at" gorm:"column:finished_at"`
	CreatedAt      time.Time       `json:"created_at" gorm:"column:created_at"`
	UpdatedAt      time.Time       `json:"updated_at" gorm:"column:updated_at"`
}

func (Run) TableName() string { return "agent_runs" }
