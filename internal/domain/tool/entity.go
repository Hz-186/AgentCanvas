package tool

import (
	"encoding/json"
	"time"
)

const (
	TypeHTTP = "http"
)

const (
	StatusDisabled = iota
	StatusActive
)

const ( // result
	InvocationStatusSucceeded = "succeeded"
	InvocationStatusFailed    = "failed"
)

type Definition struct {
	ID               int64           `json:"id" gorm:"primaryKey;column:id"`
	OwnerID          int64           `json:"owner_id" gorm:"column:owner_id"`
	Name             string          `json:"name" gorm:"column:name"`
	ToolType         string          `json:"tool_type" gorm:"column:tool_type"`
	Description      string          `json:"description" gorm:"column:description"`
	ConfigJSON       json.RawMessage `json:"config_json" gorm:"column:config_json"`               // example: {"method":"GET","url":"https://api.example.com/weather"}
	InputSchemaJSON  json.RawMessage `json:"input_schema_json" gorm:"column:input_schema_json"`   // input JSON Schema
	OutputSchemaJSON json.RawMessage `json:"output_schema_json" gorm:"column:output_schema_json"` // output JSON Schema
	Status           int             `json:"status" gorm:"column:status"`
	CreatedAt        time.Time       `json:"created_at" gorm:"column:created_at"`
	UpdatedAt        time.Time       `json:"updated_at" gorm:"column:updated_at"`
	DeletedAt        *time.Time      `json:"-" gorm:"column:deleted_at"`
}

func (Definition) TableName() string { return "tool_definitions" }

type Invocation struct { // log of using tool
	ID           int64           `json:"id" gorm:"primaryKey;column:id"`
	OwnerID      int64           `json:"owner_id" gorm:"column:owner_id"`
	RunID        int64           `json:"run_id" gorm:"column:run_id"`
	NodeID       string          `json:"node_id" gorm:"column:node_id"`
	ToolID       int64           `json:"tool_id" gorm:"column:tool_id"`
	ToolName     string          `json:"tool_name" gorm:"column:tool_name"`
	ToolType     string          `json:"tool_type" gorm:"column:tool_type"`
	InputJSON    json.RawMessage `json:"input_json" gorm:"column:input_json"`
	OutputJSON   json.RawMessage `json:"output_json" gorm:"column:output_json"`
	Status       string          `json:"status" gorm:"column:status"`
	ErrorMessage string          `json:"error_message" gorm:"column:error_message"`
	LatencyMS    int             `json:"latency_ms" gorm:"column:latency_ms"`
	CreatedAt    time.Time       `json:"created_at" gorm:"column:created_at"`
}

func (Invocation) TableName() string { return "tool_invocations" }
