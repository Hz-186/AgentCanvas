package workflow

import (
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

type Profile struct {
	ID                          int64           `json:"id" gorm:"primaryKey;column:id"`
	OwnerID                     int64           `json:"owner_id" gorm:"column:owner_id"`
	WorkflowID                  int64           `json:"workflow_id" gorm:"column:workflow_id"`
	Role                        string          `json:"role" gorm:"column:role"`
	Goal                        string          `json:"goal" gorm:"column:goal"`
	Backstory                   string          `json:"backstory" gorm:"column:backstory"`
	SystemPrompt                string          `json:"system_prompt" gorm:"column:system_prompt"`
	DefaultProviderID           *int64          `json:"default_provider_id" gorm:"column:default_provider_id"`
	DefaultModel                string          `json:"default_model" gorm:"column:default_model"`
	MaxIterations               int             `json:"max_iterations" gorm:"column:max_iterations"`
	MaxExecutionTimeMS          int             `json:"max_execution_time_ms" gorm:"column:max_execution_time_ms"`
	MemoryEnabled               bool            `json:"memory_enabled" gorm:"column:memory_enabled"`
	PlanningEnabled             bool            `json:"planning_enabled" gorm:"column:planning_enabled"`
	AllowDelegation             bool            `json:"allow_delegation" gorm:"column:allow_delegation"`
	AllowCodeExecution          bool            `json:"allow_code_execution" gorm:"column:allow_code_execution"`
	DefaultToolPackIDs          json.RawMessage `json:"default_tool_pack_ids" gorm:"column:default_tool_pack_ids"`
	DefaultToolIDs              json.RawMessage `json:"default_tool_ids" gorm:"column:default_tool_ids"`
	DefaultMCPServerIDs         json.RawMessage `json:"default_mcp_server_ids" gorm:"column:default_mcp_server_ids"`
	DefaultKnowledgeIDs         json.RawMessage `json:"default_knowledge_ids" gorm:"column:default_knowledge_ids"`
	DefaultKnowledgeTopK        int             `json:"default_knowledge_top_k" gorm:"column:default_knowledge_top_k"`
	DefaultKnowledgeMode        string          `json:"default_knowledge_mode" gorm:"column:default_knowledge_mode"`
	DefaultCallWorkflowIDs      json.RawMessage `json:"default_call_workflow_ids" gorm:"column:default_call_workflow_ids"`
	DefaultMaxWorkflowCallDepth int             `json:"default_max_workflow_call_depth" gorm:"column:default_max_workflow_call_depth"`
	OutputSchemaJSON            json.RawMessage `json:"output_schema_json" gorm:"column:output_schema_json"`
	CreatedAt                   time.Time       `json:"created_at" gorm:"column:created_at"`
	UpdatedAt                   time.Time       `json:"updated_at" gorm:"column:updated_at"`
	DeletedAt                   *time.Time      `json:"-" gorm:"column:deleted_at"`
}

func (Profile) TableName() string { return "workflow_profiles" }

func (p *Profile) DefaultToolIDsSlice() []int64 {
	return decodeInt64Slice(p.DefaultToolIDs)
}

func (p *Profile) DefaultToolPackIDsSlice() []int64 {
	return decodeInt64Slice(p.DefaultToolPackIDs)
}

func (p *Profile) DefaultMCPServerIDsSlice() []int64 {
	return decodeInt64Slice(p.DefaultMCPServerIDs)
}

func (p *Profile) DefaultKnowledgeIDsSlice() []int64 {
	return decodeInt64Slice(p.DefaultKnowledgeIDs)
}

func (p *Profile) DefaultCallWorkflowIDsSlice() []int64 {
	return decodeInt64Slice(p.DefaultCallWorkflowIDs)
}

func (p *Profile) BeforeCreate(tx *gorm.DB) error {
	return p.normalizeJSON()
}

func (p *Profile) BeforeUpdate(tx *gorm.DB) error {
	return p.normalizeJSON()
}

func (p *Profile) normalizeJSON() error {
	p.DefaultToolPackIDs = normalizeJSONField(p.DefaultToolPackIDs)
	p.DefaultToolIDs = normalizeJSONField(p.DefaultToolIDs)
	p.DefaultMCPServerIDs = normalizeJSONField(p.DefaultMCPServerIDs)
	p.DefaultKnowledgeIDs = normalizeJSONField(p.DefaultKnowledgeIDs)
	p.DefaultCallWorkflowIDs = normalizeJSONField(p.DefaultCallWorkflowIDs)
	p.OutputSchemaJSON = normalizeJSONObjectField(p.OutputSchemaJSON)
	return nil
}

func decodeInt64Slice(raw json.RawMessage) []int64 {
	if len(raw) == 0 {
		return nil
	}
	var ids []int64
	if err := json.Unmarshal(raw, &ids); err != nil {
		return nil
	}
	return ids
}

func normalizeJSONField(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage("[]")
	}
	return raw
}

func normalizeJSONObjectField(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return json.RawMessage("{}")
	}
	return raw
}
