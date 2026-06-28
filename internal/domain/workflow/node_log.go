package workflow

import (
	"encoding/json"
	"time"
)

const (
	NodeLogStatusRunning   = "running"
	NodeLogStatusSucceeded = "succeeded"
	NodeLogStatusFailed    = "failed"
)

type NodeLog struct {
	ID           int64           `json:"id" gorm:"primaryKey;column:id"`
	OwnerID      int64           `json:"owner_id" gorm:"column:owner_id"`
	RunID        int64           `json:"run_id" gorm:"column:run_id"`
	NodeID       string          `json:"node_id" gorm:"column:node_id"`
	NodeType     string          `json:"node_type" gorm:"column:node_type"`
	Status       string          `json:"status" gorm:"column:status"`
	InputJSON    json.RawMessage `json:"input_json" gorm:"column:input_json"`
	OutputJSON   json.RawMessage `json:"output_json" gorm:"column:output_json"`
	ErrorMessage string          `json:"error_message" gorm:"column:error_message"`
	TokenCount   int             `json:"token_count" gorm:"column:token_count"`
	LatencyMS    int             `json:"latency_ms" gorm:"column:latency_ms"`
	StartedAt    time.Time       `json:"started_at" gorm:"column:started_at"`
	FinishedAt   *time.Time      `json:"finished_at" gorm:"column:finished_at"`
	CreatedAt    time.Time       `json:"created_at" gorm:"column:created_at"`
}

func (NodeLog) TableName() string { return "workflow_node_logs" }
