package workflow

import (
	"encoding/json"
	"time"
)

const (
	RunKindWorkflow          = "workflow"
	RunKindInlineAgent       = "inline_agent"
	RunKindAgent             = "agent"
	RunKindLifecycleWorkflow = "lifecycle_workflow"

	RunStatusRunning      = "running"
	RunStatusQueued       = "queued"
	RunStatusSucceeded    = "succeeded"
	RunStatusFailed       = "failed"
	RunStatusCancelled    = "cancelled"
	RunStatusWaitingHuman = "waiting_human"
	RunStatusPaused       = "paused"
	RunStatusResuming     = "resuming"
	RunStatusTimeout      = "timeout"
)

type Run struct {
	ID               int64           `json:"id" gorm:"primaryKey;column:id"`
	OwnerID          int64           `json:"owner_id" gorm:"column:owner_id"`
	WorkflowID       int64           `json:"workflow_id" gorm:"column:workflow_id"`
	FlowVersionID    int64           `json:"flow_version_id" gorm:"column:flow_version_id"`
	AgentID          *int64          `json:"agent_id,omitempty" gorm:"column:agent_id"`
	AgentReleaseID   *int64          `json:"agent_release_id,omitempty" gorm:"column:agent_release_id"`
	RuleSetID        *int64          `json:"rule_set_id,omitempty" gorm:"column:rule_set_id"`
	RuleSetVersion   string          `json:"rule_set_version,omitempty" gorm:"column:rule_set_version"`
	CompiledRuleHash string          `json:"compiled_rule_hash,omitempty" gorm:"column:compiled_rule_hash"`
	RunKind          string          `json:"run_kind" gorm:"column:run_kind"`
	DefinitionJSON   json.RawMessage `json:"definition_json,omitempty" gorm:"column:definition_json"`
	DefinitionHash   string          `json:"definition_hash,omitempty" gorm:"column:definition_hash"`
	ConversationID   *int64          `json:"conversation_id" gorm:"column:conversation_id"`
	ParentRunID      *int64          `json:"parent_run_id" gorm:"column:parent_run_id"`
	CallerNodeID     string          `json:"caller_node_id" gorm:"column:caller_node_id"`
	CallDepth        int             `json:"call_depth" gorm:"column:call_depth"`
	CallChainJSON    json.RawMessage `json:"call_chain_json" gorm:"column:call_chain_json"`
	Status           string          `json:"status" gorm:"column:status"`
	InputJSON        json.RawMessage `json:"input_json" gorm:"column:input_json"`
	OutputJSON       json.RawMessage `json:"output_json" gorm:"column:output_json"`
	ErrorMessage     string          `json:"error_message" gorm:"column:error_message"`
	TotalTokens      int             `json:"total_tokens" gorm:"column:total_tokens"`
	LatencyMS        int             `json:"latency_ms" gorm:"column:latency_ms"`
	StartedAt        time.Time       `json:"started_at" gorm:"column:started_at"`
	FinishedAt       *time.Time      `json:"finished_at" gorm:"column:finished_at"`
	CreatedAt        time.Time       `json:"created_at" gorm:"column:created_at"`
	UpdatedAt        time.Time       `json:"updated_at" gorm:"column:updated_at"`
}

func (Run) TableName() string { return "workflow_runs" }
