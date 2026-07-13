package workflow

import (
	"encoding/json"
	"time"
)

const (
	RuleSetStatusDraft          = "draft"
	RuleSetStatusQueued         = "queued"
	RuleSetStatusCompiling      = "compiling"
	RuleSetStatusReviewRequired = "review_required"
	RuleSetStatusReady          = "ready"
	RuleSetStatusPublished      = "published"
	RuleSetStatusSuperseded     = "superseded"
	RuleSetStatusFailed         = "failed"

	RuleEdgeDecisionPending  = "pending"
	RuleEdgeDecisionAccepted = "accepted"
	RuleEdgeDecisionRejected = "rejected"

	RuleCompileJobType      = "rule_compile"
	RuleCompileJobQueued    = "queued"
	RuleCompileJobCompiling = "compiling"
	RuleCompileJobCompleted = "completed"
	RuleCompileJobFailed    = "failed"
	RuleCompileJobStale     = "stale"
)

type RuleSet struct {
	ID                    int64           `json:"id" gorm:"primaryKey;column:id"`
	OwnerID               int64           `json:"owner_id" gorm:"column:owner_id"`
	WorkflowID            int64           `json:"workflow_id" gorm:"column:workflow_id"`
	VersionNo             int             `json:"version_no" gorm:"column:version_no"`
	Status                string          `json:"status" gorm:"column:status"`
	Revision              int64           `json:"revision" gorm:"column:revision"`
	SourceHash            string          `json:"source_hash" gorm:"column:source_hash"`
	CompiledHash          string          `json:"compiled_hash" gorm:"column:compiled_hash"`
	CompiledSnapshotJSON  json.RawMessage `json:"compiled_snapshot_json,omitempty" gorm:"column:compiled_snapshot_json"`
	CompilerProviderID    *int64          `json:"compiler_provider_id" gorm:"column:compiler_provider_id"`
	CompilerModel         string          `json:"compiler_model" gorm:"column:compiler_model"`
	CompilerPromptVersion string          `json:"compiler_prompt_version" gorm:"column:compiler_prompt_version"`
	TokenEstimatorVersion string          `json:"token_estimator_version" gorm:"column:token_estimator_version"`
	RollbackOfRuleSetID   *int64          `json:"rollback_of_rule_set_id,omitempty" gorm:"column:rollback_of_rule_set_id"`
	CompileError          string          `json:"compile_error,omitempty" gorm:"column:compile_error"`
	PublishedBy           *int64          `json:"published_by,omitempty" gorm:"column:published_by"`
	PublishedAt           *time.Time      `json:"published_at,omitempty" gorm:"column:published_at"`
	CreatedAt             time.Time       `json:"created_at" gorm:"column:created_at"`
	UpdatedAt             time.Time       `json:"updated_at" gorm:"column:updated_at"`
	Nodes                 []RuleNode      `json:"rules,omitempty" gorm:"-"`
	Edges                 []RuleEdge      `json:"edges,omitempty" gorm:"-"`
}

func (RuleSet) TableName() string { return "workflow_rule_sets" }

type RuleNode struct {
	ID                int64           `json:"-" gorm:"primaryKey;column:id"`
	RuleSetID         int64           `json:"-" gorm:"column:rule_set_id"`
	RuleID            string          `json:"id" gorm:"column:rule_id"`
	Name              string          `json:"name" gorm:"column:name"`
	Content           string          `json:"content" gorm:"column:content"`
	Strength          string          `json:"strength" gorm:"column:strength"`
	ActivationJSON    json.RawMessage `json:"activation,omitempty" gorm:"column:activation_json"`
	Priority          int             `json:"priority" gorm:"column:priority"`
	SafetyCritical    bool            `json:"safety_critical" gorm:"column:safety_critical"`
	PolicyBindingJSON json.RawMessage `json:"policy_binding,omitempty" gorm:"column:policy_binding_json"`
	TokenCost         int             `json:"token_cost,omitempty" gorm:"column:token_cost"`
	TopologicalOrder  int             `json:"topological_order,omitempty" gorm:"column:topological_order"`
	ContentHash       string          `json:"content_hash,omitempty" gorm:"column:content_hash"`
	CreatedAt         time.Time       `json:"-" gorm:"column:created_at"`
	UpdatedAt         time.Time       `json:"-" gorm:"column:updated_at"`
}

func (RuleNode) TableName() string { return "workflow_rule_nodes" }

type RuleEdge struct {
	ID              int64     `json:"id" gorm:"primaryKey;column:id"`
	RuleSetID       int64     `json:"-" gorm:"column:rule_set_id"`
	RuleID          string    `json:"rule_id" gorm:"column:rule_id"`
	DependsOnRuleID string    `json:"depends_on" gorm:"column:depends_on_rule_id"`
	Source          string    `json:"source" gorm:"column:source"`
	Confidence      float64   `json:"confidence" gorm:"column:confidence"`
	Reason          string    `json:"reason,omitempty" gorm:"column:reason"`
	Decision        string    `json:"decision" gorm:"column:decision"`
	CreatedAt       time.Time `json:"-" gorm:"column:created_at"`
	UpdatedAt       time.Time `json:"-" gorm:"column:updated_at"`
}

func (RuleEdge) TableName() string { return "workflow_rule_edges" }

type RuleCompileJob struct {
	ID                 int64      `json:"id" gorm:"primaryKey;column:id"`
	OwnerID            int64      `json:"owner_id" gorm:"column:owner_id"`
	WorkflowID         int64      `json:"workflow_id" gorm:"column:workflow_id"`
	RuleSetID          int64      `json:"rule_set_id" gorm:"column:rule_set_id"`
	Revision           int64      `json:"revision" gorm:"column:revision"`
	SourceHash         string     `json:"source_hash" gorm:"column:source_hash"`
	Status             string     `json:"status" gorm:"column:status"`
	Attempts           int        `json:"attempts" gorm:"column:attempts"`
	WorkerID           string     `json:"worker_id,omitempty" gorm:"column:worker_id"`
	IdempotencyKey     string     `json:"idempotency_key" gorm:"column:idempotency_key"`
	CompilerProviderID *int64     `json:"compiler_provider_id" gorm:"column:compiler_provider_id"`
	CompilerModel      string     `json:"compiler_model" gorm:"column:compiler_model"`
	PromptTokens       int        `json:"prompt_tokens" gorm:"column:prompt_tokens"`
	CompletionTokens   int        `json:"completion_tokens" gorm:"column:completion_tokens"`
	ErrorMessage       string     `json:"error_message,omitempty" gorm:"column:error_message"`
	AvailableAt        time.Time  `json:"available_at" gorm:"column:available_at"`
	StartedAt          *time.Time `json:"started_at,omitempty" gorm:"column:started_at"`
	FinishedAt         *time.Time `json:"finished_at,omitempty" gorm:"column:finished_at"`
	CreatedAt          time.Time  `json:"created_at" gorm:"column:created_at"`
	UpdatedAt          time.Time  `json:"updated_at" gorm:"column:updated_at"`
}

func (RuleCompileJob) TableName() string { return "workflow_rule_compile_jobs" }
