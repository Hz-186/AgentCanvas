package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	StatusDraft    = "draft"
	StatusActive   = "active"
	StatusArchived = "archived"

	TurnStatusQueued       = "queued"
	TurnStatusRunning      = "running"
	TurnStatusWaitingHuman = "waiting_human"
	TurnStatusPaused       = "paused"
	TurnStatusSucceeded    = "succeeded"
	TurnStatusFailed       = "failed"
	TurnStatusCancelled    = "cancelled"
	TurnStatusRetryWait    = "retry_wait"
)

// Definition is the complete, workflow-independent configuration used by an
// Agent release. Keep all resource references here so a release can be pinned
// and reproduced without consulting a Workflow profile.
type Definition struct {
	ProviderID                int64           `json:"provider_id"`
	Model                     string          `json:"model"`
	SystemPrompt              string          `json:"system_prompt"`
	Role                      string          `json:"role,omitempty"`
	Goal                      string          `json:"goal,omitempty"`
	Backstory                 string          `json:"backstory,omitempty"`
	Mode                      string          `json:"mode"`
	Temperature               *float64        `json:"temperature,omitempty"`
	ToolPackIDs               []int64         `json:"tool_pack_ids,omitempty"`
	ToolIDs                   []int64         `json:"tool_ids,omitempty"`
	SkillIDs                  []int64         `json:"skill_ids,omitempty"`
	SkillLoadingMode          string          `json:"skill_loading_mode,omitempty"`
	KnowledgeIDs              []int64         `json:"knowledge_ids,omitempty"`
	KnowledgeTopK             int             `json:"knowledge_top_k,omitempty"`
	KnowledgeMode             string          `json:"knowledge_mode,omitempty"`
	MCPServerIDs              []int64         `json:"mcp_server_ids,omitempty"`
	CallableAgentIDs          []int64         `json:"callable_agent_ids,omitempty"`
	CallableWorkflowIDs       []int64         `json:"call_workflow_ids,omitempty"`
	AllowInlineAgents         bool            `json:"allow_inline_agents,omitempty"`
	MaxParallelSubAgents      int             `json:"max_parallel_sub_agents,omitempty"`
	MaxWorkflowCallDepth      int             `json:"max_workflow_call_depth,omitempty"`
	MemoryEnabled             bool            `json:"memory_enabled,omitempty"`
	ReflectionEnabled         bool            `json:"reflection_enabled,omitempty"`
	MemoryPolicyJSON          json.RawMessage `json:"memory_policy_json,omitempty"`
	ReflectionPolicyJSON      json.RawMessage `json:"reflection_policy_json,omitempty"`
	ToolPolicyJSON            json.RawMessage `json:"tool_policy_json,omitempty"`
	ContextPolicyJSON         json.RawMessage `json:"context_policy_json,omitempty"`
	RulesJSON                 json.RawMessage `json:"rules_json,omitempty"`
	OutputSchemaJSON          json.RawMessage `json:"output_schema_json,omitempty"`
	OutputMode                string          `json:"output_mode,omitempty"`
	ReturnIntermediateSteps   bool            `json:"return_intermediate_steps,omitempty"`
	MaxIterations             int             `json:"max_iterations,omitempty"`
	MaxToolCalls              int             `json:"max_tool_calls,omitempty"`
	MaxExecutionTimeMS        int             `json:"max_execution_time_ms,omitempty"`
	MaxToolTimeoutMS          int             `json:"max_tool_timeout_ms,omitempty"`
	MaxToolOutputBytes        int             `json:"max_tool_output_bytes,omitempty"`
	MaxInputChars             int             `json:"max_input_chars,omitempty"`
	MaxInputTokens            int             `json:"max_input_tokens,omitempty"`
	ContextWindowTokens       int             `json:"context_window_tokens,omitempty"`
	ReservedOutputTokens      int             `json:"reserved_output_tokens,omitempty"`
	ContextSafetyMarginTokens int             `json:"context_safety_margin_tokens,omitempty"`
	MaxRuleTokens             int             `json:"max_rule_tokens,omitempty"`
	WorkspaceEnabled          bool            `json:"workspace_enabled,omitempty"`
	WorkspacePackID           *int64          `json:"workspace_pack_id,omitempty"`
	PreTurnWorkflowID         *int64          `json:"pre_turn_workflow_id,omitempty"`
	PreTurnWorkflowVersionID  *int64          `json:"pre_turn_workflow_version_id,omitempty"`
	PostTurnWorkflowID        *int64          `json:"post_turn_workflow_id,omitempty"`
	PostTurnWorkflowVersionID *int64          `json:"post_turn_workflow_version_id,omitempty"`
}

func (d Definition) Normalize() Definition {
	if strings.TrimSpace(d.Mode) == "" {
		d.Mode = "react"
	}
	if d.MaxIterations == 0 {
		d.MaxIterations = 8
	}
	if d.MaxToolCalls == 0 {
		d.MaxToolCalls = 16
	}
	if d.MaxExecutionTimeMS == 0 {
		d.MaxExecutionTimeMS = 120000
	}
	if d.MaxToolTimeoutMS == 0 {
		d.MaxToolTimeoutMS = 30000
	}
	if d.MaxToolOutputBytes == 0 {
		d.MaxToolOutputBytes = 512 * 1024
	}
	if d.MaxParallelSubAgents == 0 {
		d.MaxParallelSubAgents = 4
	}
	if d.MaxWorkflowCallDepth == 0 {
		d.MaxWorkflowCallDepth = 3
	}
	if d.KnowledgeTopK == 0 {
		d.KnowledgeTopK = 5
	}
	if strings.TrimSpace(d.KnowledgeMode) == "" {
		d.KnowledgeMode = "hybrid"
	}
	if strings.TrimSpace(d.SkillLoadingMode) == "" {
		d.SkillLoadingMode = "auto"
	}
	if strings.TrimSpace(d.OutputMode) == "" {
		d.OutputMode = "final_answer"
	}
	return d
}

func (d Definition) Validate() error {
	d = d.Normalize()
	if d.ProviderID <= 0 {
		return fmt.Errorf("provider_id is required")
	}
	if d.Mode != "react" && d.Mode != "plan_execute" {
		return fmt.Errorf("mode must be react or plan_execute")
	}
	if d.Temperature != nil && (*d.Temperature < 0 || *d.Temperature > 2) {
		return fmt.Errorf("temperature must be 0..2")
	}
	if d.SkillLoadingMode != "auto" && d.SkillLoadingMode != "metadata_only" && d.SkillLoadingMode != "search" {
		return fmt.Errorf("skill_loading_mode must be auto, metadata_only or search")
	}
	if d.OutputMode != "final_answer" && d.OutputMode != "full" {
		return fmt.Errorf("output_mode must be final_answer or full")
	}
	if d.MaxIterations < 1 || d.MaxIterations > 50 {
		return fmt.Errorf("max_iterations must be 1..50")
	}
	if d.MaxToolCalls < 1 || d.MaxToolCalls > 100 {
		return fmt.Errorf("max_tool_calls must be 1..100")
	}
	if d.MaxExecutionTimeMS < 1 || d.MaxExecutionTimeMS > 600000 {
		return fmt.Errorf("max_execution_time_ms must be 1..600000")
	}
	if d.MaxParallelSubAgents < 1 || d.MaxParallelSubAgents > 64 {
		return fmt.Errorf("max_parallel_sub_agents must be 1..64")
	}
	if d.MaxWorkflowCallDepth < 1 || d.MaxWorkflowCallDepth > 5 {
		return fmt.Errorf("max_workflow_call_depth must be 1..5")
	}
	if d.KnowledgeTopK < 1 || d.KnowledgeTopK > 20 {
		return fmt.Errorf("knowledge_top_k must be 1..20")
	}
	if d.KnowledgeMode != "keyword" && d.KnowledgeMode != "vector" && d.KnowledgeMode != "hybrid" {
		return fmt.Errorf("knowledge_mode must be keyword, vector or hybrid")
	}
	if d.MaxToolTimeoutMS < 1 || d.MaxToolTimeoutMS > 600000 {
		return fmt.Errorf("max_tool_timeout_ms must be 1..600000")
	}
	if d.MaxToolOutputBytes < 1024 || d.MaxToolOutputBytes > 2*1024*1024 {
		return fmt.Errorf("max_tool_output_bytes must be 1024..2097152")
	}
	if d.MaxInputChars < 0 || d.MaxInputTokens < 0 || d.ContextWindowTokens < 0 || d.ReservedOutputTokens < 0 || d.ContextSafetyMarginTokens < 0 || d.MaxRuleTokens < 0 {
		return fmt.Errorf("context limits must not be negative")
	}
	if d.WorkspaceEnabled && d.WorkspacePackID == nil {
		return fmt.Errorf("workspace_pack_id is required when workspace is enabled")
	}
	if (d.PreTurnWorkflowID == nil) != (d.PreTurnWorkflowVersionID == nil) {
		return fmt.Errorf("pre_turn_workflow_id and pre_turn_workflow_version_id must be configured together")
	}
	if (d.PostTurnWorkflowID == nil) != (d.PostTurnWorkflowVersionID == nil) {
		return fmt.Errorf("post_turn_workflow_id and post_turn_workflow_version_id must be configured together")
	}
	return nil
}

func (d Definition) Snapshot() (json.RawMessage, string, error) {
	d = d.Normalize()
	if err := d.Validate(); err != nil {
		return nil, "", err
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(raw)
	return raw, hex.EncodeToString(sum[:]), nil
}

func (d Definition) ResourceSnapshot() (json.RawMessage, string, string, error) {
	resources := map[string]any{
		"tool_pack_ids": d.ToolPackIDs, "tool_ids": d.ToolIDs, "skill_ids": d.SkillIDs,
		"knowledge_ids": d.KnowledgeIDs, "mcp_server_ids": d.MCPServerIDs,
		"callable_agent_ids": d.CallableAgentIDs, "call_workflow_ids": d.CallableWorkflowIDs,
		"pre_turn_workflow_version_id":  d.PreTurnWorkflowVersionID,
		"post_turn_workflow_version_id": d.PostTurnWorkflowVersionID,
	}
	raw, err := json.Marshal(resources)
	if err != nil {
		return nil, "", "", err
	}
	toolRaw, err := json.Marshal(map[string]any{"tool_pack_ids": d.ToolPackIDs, "tool_ids": d.ToolIDs, "mcp_server_ids": d.MCPServerIDs})
	if err != nil {
		return nil, "", "", err
	}
	toolSum, ruleSum := sha256.Sum256(toolRaw), sha256.Sum256(d.RulesJSON)
	return raw, hex.EncodeToString(ruleSum[:]), hex.EncodeToString(toolSum[:]), nil
}

type Agent struct {
	ID                  int64           `json:"id" gorm:"primaryKey;column:id"`
	OwnerID             int64           `json:"owner_id" gorm:"column:owner_id"`
	Name                string          `json:"name" gorm:"column:name"`
	Description         string          `json:"description" gorm:"column:description"`
	AvatarURL           string          `json:"avatar_url" gorm:"column:avatar_url"`
	Status              string          `json:"status" gorm:"column:status"`
	DraftDefinitionJSON json.RawMessage `json:"-" gorm:"column:draft_definition_json"`
	DraftDefinition     Definition      `json:"definition" gorm:"-"`
	CurrentReleaseID    *int64          `json:"current_release_id,omitempty" gorm:"column:current_release_id"`
	LegacyDialogID      *int64          `json:"legacy_dialog_id,omitempty" gorm:"column:legacy_dialog_id"`
	CreatedAt           time.Time       `json:"created_at" gorm:"column:created_at"`
	UpdatedAt           time.Time       `json:"updated_at" gorm:"column:updated_at"`
	DeletedAt           *time.Time      `json:"-" gorm:"column:deleted_at"`
}

func (Agent) TableName() string { return "agents" }

type Release struct {
	ID               int64           `json:"id" gorm:"primaryKey;column:id"`
	OwnerID          int64           `json:"owner_id" gorm:"column:owner_id"`
	AgentID          int64           `json:"agent_id" gorm:"column:agent_id"`
	VersionNo        int             `json:"version_no" gorm:"column:version_no"`
	DefinitionJSON   json.RawMessage `json:"-" gorm:"column:definition_json"`
	Definition       Definition      `json:"definition" gorm:"-"`
	Checksum         string          `json:"checksum" gorm:"column:checksum"`
	RuleSetHash      string          `json:"rule_set_hash" gorm:"column:rule_set_hash"`
	ToolSchemaHash   string          `json:"tool_schema_hash" gorm:"column:tool_schema_hash"`
	ResourceVersions json.RawMessage `json:"resource_versions" gorm:"column:resource_versions_json"`
	CreatedBy        int64           `json:"created_by" gorm:"column:created_by"`
	CreatedAt        time.Time       `json:"created_at" gorm:"column:created_at"`
}

func (Release) TableName() string { return "agent_releases" }

type Turn struct {
	ID                 int64           `json:"id" gorm:"primaryKey;column:id"`
	OwnerID            int64           `json:"owner_id" gorm:"column:owner_id"`
	AgentID            int64           `json:"agent_id" gorm:"column:agent_id"`
	AgentReleaseID     int64           `json:"agent_release_id" gorm:"column:agent_release_id"`
	ConversationID     int64           `json:"conversation_id" gorm:"column:conversation_id"`
	RunID              *int64          `json:"run_id,omitempty" gorm:"column:run_id"`
	UserMessageID      int64           `json:"user_message_id" gorm:"column:user_message_id"`
	AssistantMessageID *int64          `json:"assistant_message_id,omitempty" gorm:"column:assistant_message_id"`
	IdempotencyKey     string          `json:"idempotency_key" gorm:"column:idempotency_key"`
	Status             string          `json:"status" gorm:"column:status"`
	InputJSON          json.RawMessage `json:"input_json" gorm:"column:input_json"`
	OutputJSON         json.RawMessage `json:"output_json" gorm:"column:output_json"`
	ErrorMessage       string          `json:"error_message" gorm:"column:error_message"`
	AttemptCount       int             `json:"attempt_count" gorm:"column:attempt_count"`
	MaxAttempts        int             `json:"max_attempts" gorm:"column:max_attempts"`
	WorkerID           string          `json:"worker_id,omitempty" gorm:"column:worker_id"`
	LeaseToken         string          `json:"-" gorm:"column:lease_token"`
	LeaseExpiresAt     *time.Time      `json:"lease_expires_at,omitempty" gorm:"column:lease_expires_at"`
	LastHeartbeatAt    *time.Time      `json:"last_heartbeat_at,omitempty" gorm:"column:last_heartbeat_at"`
	RetryAt            *time.Time      `json:"retry_at,omitempty" gorm:"column:retry_at"`
	StartedAt          *time.Time      `json:"started_at,omitempty" gorm:"column:started_at"`
	FinishedAt         *time.Time      `json:"finished_at,omitempty" gorm:"column:finished_at"`
	CreatedAt          time.Time       `json:"created_at" gorm:"column:created_at"`
	UpdatedAt          time.Time       `json:"updated_at" gorm:"column:updated_at"`
}

const (
	ReviewStatusPending   = "pending"
	ReviewStatusRunning   = "running"
	ReviewStatusCompleted = "completed"
	ReviewStatusFailed    = "failed"

	ProposalKindMemory     = "memory"
	ProposalKindReflection = "reflection"
	ProposalKindSkill      = "skill"
	ProposalKindRule       = "rule"

	ProposalStatusPending          = "pending"
	ProposalStatusApproved         = "approved"
	ProposalStatusRejected         = "rejected"
	ProposalStatusApplied          = "applied"
	ProposalStatusRejectedSecurity = "rejected_security"
)

type ImprovementReview struct {
	ID              int64      `json:"id" gorm:"primaryKey;column:id"`
	OwnerID         int64      `json:"owner_id" gorm:"column:owner_id"`
	AgentID         int64      `json:"agent_id" gorm:"column:agent_id"`
	AgentReleaseID  int64      `json:"agent_release_id" gorm:"column:agent_release_id"`
	ConversationID  int64      `json:"conversation_id" gorm:"column:conversation_id"`
	TurnID          int64      `json:"turn_id" gorm:"column:turn_id"`
	RunID           int64      `json:"run_id" gorm:"column:run_id"`
	ProviderID      int64      `json:"provider_id" gorm:"column:provider_id"`
	Model           string     `json:"model" gorm:"column:model"`
	Status          string     `json:"status" gorm:"column:status"`
	AttemptCount    int        `json:"attempt_count" gorm:"column:attempt_count"`
	MaxAttempts     int        `json:"max_attempts" gorm:"column:max_attempts"`
	WorkerID        string     `json:"worker_id,omitempty" gorm:"column:worker_id"`
	LeaseToken      string     `json:"-" gorm:"column:lease_token"`
	LeaseExpiresAt  *time.Time `json:"lease_expires_at,omitempty" gorm:"column:lease_expires_at"`
	LastHeartbeatAt *time.Time `json:"last_heartbeat_at,omitempty" gorm:"column:last_heartbeat_at"`
	RetryAt         *time.Time `json:"retry_at,omitempty" gorm:"column:retry_at"`
	ErrorMessage    string     `json:"error_message,omitempty" gorm:"column:error_message"`
	CreatedAt       time.Time  `json:"created_at" gorm:"column:created_at"`
	UpdatedAt       time.Time  `json:"updated_at" gorm:"column:updated_at"`
	CompletedAt     *time.Time `json:"completed_at,omitempty" gorm:"column:completed_at"`
}

func (ImprovementReview) TableName() string { return "agent_improvement_reviews" }

type ChangeProposal struct {
	ID             int64           `json:"id" gorm:"primaryKey;column:id"`
	OwnerID        int64           `json:"owner_id" gorm:"column:owner_id"`
	AgentID        int64           `json:"agent_id" gorm:"column:agent_id"`
	ReviewID       int64           `json:"review_id" gorm:"column:review_id"`
	TurnID         int64           `json:"turn_id" gorm:"column:turn_id"`
	RunID          int64           `json:"run_id" gorm:"column:run_id"`
	Kind           string          `json:"kind" gorm:"column:kind"`
	Title          string          `json:"title" gorm:"column:title"`
	Content        string          `json:"content" gorm:"column:content"`
	PayloadJSON    json.RawMessage `json:"payload_json" gorm:"column:payload_json"`
	EvidenceJSON   json.RawMessage `json:"evidence_json" gorm:"column:evidence_json"`
	DiffJSON       json.RawMessage `json:"diff_json" gorm:"column:diff_json"`
	Confidence     float64         `json:"confidence" gorm:"column:confidence"`
	Checksum       string          `json:"checksum" gorm:"column:checksum"`
	SecurityStatus string          `json:"security_status" gorm:"column:security_status"`
	SecurityReason string          `json:"security_reason,omitempty" gorm:"column:security_reason"`
	Status         string          `json:"status" gorm:"column:status"`
	DecisionNote   string          `json:"decision_note,omitempty" gorm:"column:decision_note"`
	DecidedBy      *int64          `json:"decided_by,omitempty" gorm:"column:decided_by"`
	DecidedAt      *time.Time      `json:"decided_at,omitempty" gorm:"column:decided_at"`
	AppliedAt      *time.Time      `json:"applied_at,omitempty" gorm:"column:applied_at"`
	CreatedAt      time.Time       `json:"created_at" gorm:"column:created_at"`
	UpdatedAt      time.Time       `json:"updated_at" gorm:"column:updated_at"`
}

func (ChangeProposal) TableName() string { return "agent_change_proposals" }

func (Turn) TableName() string { return "agent_turns" }
