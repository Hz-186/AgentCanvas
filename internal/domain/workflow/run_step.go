package workflow

import (
	"encoding/json"
	"time"
)

type RunStep struct {
	ID            int64           `json:"id" gorm:"primaryKey;column:id"`
	OwnerID       int64           `json:"owner_id" gorm:"column:owner_id"`
	RunID         int64           `json:"run_id" gorm:"column:run_id"`
	NodeID        string          `json:"node_id" gorm:"column:node_id"`
	StepIndex     int             `json:"step_index" gorm:"column:step_index"`
	StepType      string          `json:"step_type" gorm:"column:step_type"`
	Role          string          `json:"role" gorm:"column:role"`
	Content       string          `json:"content" gorm:"column:content"`
	ToolCallID    string          `json:"tool_call_id" gorm:"column:tool_call_id"`
	ToolName      string          `json:"tool_name" gorm:"column:tool_name"`
	ArgumentsJSON json.RawMessage `json:"arguments_json" gorm:"column:arguments_json"`
	OutputJSON    json.RawMessage `json:"output_json" gorm:"column:output_json"`
	ErrorMessage  string          `json:"error_message" gorm:"column:error_message"`
	TokenCount    int             `json:"token_count" gorm:"column:token_count"`
	LatencyMS     int             `json:"latency_ms" gorm:"column:latency_ms"`
	CreatedAt     time.Time       `json:"created_at" gorm:"column:created_at"`
}

func (RunStep) TableName() string { return "workflow_run_steps" }
