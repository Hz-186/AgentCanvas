package reflection

import (
	"encoding/json"
	"time"
)

const (
	ScopeNode     = "node"
	ScopeWorkflow = "workflow"
	ScopeAgent    = "agent"
	ScopeGlobal   = "global"

	KindErrorLesson       = "error_lesson"
	KindImportantStrategy = "important_strategy"

	StatusCandidate  = "candidate"
	StatusActive     = "active"
	StatusValidated  = "validated"
	StatusDisputed   = "disputed"
	StatusSuperseded = "superseded"
	StatusArchived   = "archived"

	JobPending   = "pending"
	JobRunning   = "running"
	JobCompleted = "completed"
	JobFailed    = "failed"

	OutboxPending    = "pending"
	OutboxPublishing = "publishing"
	OutboxPublished  = "published"
	OutboxJob        = "job"
	OutboxDLQ        = "dlq"

	FailureRetryable = "retryable"
	FailurePermanent = "permanent"
	FailureExhausted = "exhausted"
)

// Reflection is an evidence-backed policy lesson derived from an Agent trajectory.
// It is separate from factual/user memory so provenance and usefulness can evolve independently.
type Reflection struct {
	ID                 int64           `json:"id" gorm:"primaryKey;column:id"`
	OwnerID            int64           `json:"owner_id" gorm:"column:owner_id"`
	WorkflowID         int64           `json:"workflow_id" gorm:"column:workflow_id"`
	AgentID            *int64          `json:"agent_id,omitempty" gorm:"column:agent_id"`
	NodeID             string          `json:"node_id" gorm:"column:node_id"`
	SourceRunID        int64           `json:"source_run_id" gorm:"column:source_run_id"`
	SupersedesID       *int64          `json:"supersedes_id,omitempty" gorm:"column:supersedes_id"`
	Scope              string          `json:"scope" gorm:"column:scope"`
	Kind               string          `json:"kind" gorm:"column:kind"`
	Status             string          `json:"status" gorm:"column:status"`
	Mode               string          `json:"mode" gorm:"column:mode"`
	TriggerType        string          `json:"trigger_type" gorm:"column:trigger_type"`
	TaskFingerprint    string          `json:"task_fingerprint" gorm:"column:task_fingerprint"`
	TaskSummary        string          `json:"task_summary" gorm:"column:task_summary"`
	RootCauseCategory  string          `json:"root_cause_category" gorm:"column:root_cause_category"`
	RootCause          string          `json:"root_cause" gorm:"column:root_cause"`
	CorrectiveAction   string          `json:"corrective_action" gorm:"column:corrective_action"`
	Lesson             string          `json:"lesson" gorm:"column:lesson"`
	Applicability      string          `json:"applicability" gorm:"column:applicability"`
	EvidenceJSON       json.RawMessage `json:"evidence_json" gorm:"column:evidence_json"`
	TagsJSON           json.RawMessage `json:"tags_json" gorm:"column:tags_json"`
	Importance         float64         `json:"importance" gorm:"column:importance"`
	Confidence         float64         `json:"confidence" gorm:"column:confidence"`
	ContentHash        string          `json:"content_hash" gorm:"column:content_hash"`
	RecallCount        int             `json:"recall_count" gorm:"column:recall_count"`
	SuccessfulUseCount int             `json:"successful_use_count" gorm:"column:successful_use_count"`
	HarmfulCount       int             `json:"harmful_count" gorm:"column:harmful_count"`
	LastRecalledAt     *time.Time      `json:"last_recalled_at,omitempty" gorm:"column:last_recalled_at"`
	ExpiresAt          *time.Time      `json:"expires_at,omitempty" gorm:"column:expires_at"`
	CreatedAt          time.Time       `json:"created_at" gorm:"column:created_at"`
	UpdatedAt          time.Time       `json:"updated_at" gorm:"column:updated_at"`
	DeletedAt          *time.Time      `json:"-" gorm:"column:deleted_at"`
}

func (Reflection) TableName() string { return "agent_reflections" }

type Job struct {
	ID              int64           `json:"id" gorm:"primaryKey;column:id"`
	OwnerID         int64           `json:"owner_id" gorm:"column:owner_id"`
	WorkflowID      int64           `json:"workflow_id" gorm:"column:workflow_id"`
	AgentID         *int64          `json:"agent_id,omitempty" gorm:"column:agent_id"`
	RunID           int64           `json:"run_id" gorm:"column:run_id"`
	NodeID          string          `json:"node_id" gorm:"column:node_id"`
	TriggerHash     string          `json:"trigger_hash" gorm:"column:trigger_hash"`
	ProviderID      int64           `json:"provider_id" gorm:"column:provider_id"`
	Model           string          `json:"model" gorm:"column:model"`
	Mode            string          `json:"mode" gorm:"column:mode"`
	Task            string          `json:"task" gorm:"column:task"`
	PayloadJSON     json.RawMessage `json:"payload_json" gorm:"column:payload_json"`
	Status          string          `json:"status" gorm:"column:status"`
	AttemptCount    int             `json:"attempt_count" gorm:"column:attempt_count"`
	MaxAttempts     int             `json:"max_attempts" gorm:"column:max_attempts"`
	LockedBy        string          `json:"locked_by" gorm:"column:locked_by"`
	LockedAt        *time.Time      `json:"locked_at,omitempty" gorm:"column:locked_at"`
	LockToken       string          `json:"-" gorm:"column:lock_token"`
	LeaseExpiresAt  *time.Time      `json:"lease_expires_at,omitempty" gorm:"column:lease_expires_at"`
	LastHeartbeatAt *time.Time      `json:"last_heartbeat_at,omitempty" gorm:"column:last_heartbeat_at"`
	DispatchSeq     int             `json:"dispatch_seq" gorm:"column:dispatch_seq"`
	RetryAt         *time.Time      `json:"retry_at,omitempty" gorm:"column:retry_at"`
	ErrorMessage    string          `json:"error_message,omitempty" gorm:"column:error_message"`
	FailureType     string          `json:"failure_type,omitempty" gorm:"column:failure_type"`
	CreatedAt       time.Time       `json:"created_at" gorm:"column:created_at"`
	UpdatedAt       time.Time       `json:"updated_at" gorm:"column:updated_at"`
	CompletedAt     *time.Time      `json:"completed_at,omitempty" gorm:"column:completed_at"`
}

func (Job) TableName() string { return "agent_reflection_jobs" }

type JobOutbox struct {
	ID           int64      `json:"id" gorm:"primaryKey;column:id"`
	EventID      string     `json:"event_id" gorm:"column:event_id"`
	JobID        int64      `json:"job_id" gorm:"column:job_id"`
	DispatchSeq  int        `json:"dispatch_seq" gorm:"column:dispatch_seq"`
	EventType    string     `json:"event_type" gorm:"column:event_type"`
	AvailableAt  time.Time  `json:"available_at" gorm:"column:available_at"`
	Status       string     `json:"status" gorm:"column:status"`
	AttemptCount int        `json:"attempt_count" gorm:"column:attempt_count"`
	LockedBy     string     `json:"locked_by" gorm:"column:locked_by"`
	LockedAt     *time.Time `json:"locked_at,omitempty" gorm:"column:locked_at"`
	PublishedAt  *time.Time `json:"published_at,omitempty" gorm:"column:published_at"`
	LastError    string     `json:"last_error,omitempty" gorm:"column:last_error"`
	CreatedAt    time.Time  `json:"created_at" gorm:"column:created_at"`
	UpdatedAt    time.Time  `json:"updated_at" gorm:"column:updated_at"`
}

func (JobOutbox) TableName() string { return "agent_reflection_job_outbox" }

type Evidence struct {
	ID            int64           `json:"id" gorm:"primaryKey;column:id"`
	ReflectionID  int64           `json:"reflection_id" gorm:"column:reflection_id"`
	JobID         *int64          `json:"job_id,omitempty" gorm:"column:job_id"`
	RunID         int64           `json:"run_id" gorm:"column:run_id"`
	CandidateHash string          `json:"candidate_hash" gorm:"column:candidate_hash"`
	EvidenceJSON  json.RawMessage `json:"evidence_json" gorm:"column:evidence_json"`
	CreatedAt     time.Time       `json:"created_at" gorm:"column:created_at"`
	UpdatedAt     time.Time       `json:"updated_at" gorm:"column:updated_at"`
}

func (Evidence) TableName() string { return "agent_reflection_evidence" }

type RecallLog struct {
	ID             int64      `json:"id" gorm:"primaryKey;column:id"`
	OwnerID        int64      `json:"owner_id" gorm:"column:owner_id"`
	ReflectionID   int64      `json:"reflection_id" gorm:"column:reflection_id"`
	RunID          int64      `json:"run_id" gorm:"column:run_id"`
	NodeID         string     `json:"node_id" gorm:"column:node_id"`
	Score          float64    `json:"score" gorm:"column:score"`
	Rank           int        `json:"rank" gorm:"column:rank"`
	InjectedTokens int        `json:"injected_tokens" gorm:"column:injected_tokens"`
	Outcome        string     `json:"outcome,omitempty" gorm:"column:outcome"`
	Verdict        string     `json:"verdict,omitempty" gorm:"column:verdict"`
	FeedbackNote   string     `json:"feedback_note,omitempty" gorm:"column:feedback_note"`
	CreatedAt      time.Time  `json:"created_at" gorm:"column:created_at"`
	UpdatedAt      time.Time  `json:"updated_at" gorm:"column:updated_at"`
	ResolvedAt     *time.Time `json:"resolved_at,omitempty" gorm:"column:resolved_at"`
}

func (RecallLog) TableName() string { return "agent_reflection_recall_logs" }

// Event is the forward-compatible seam for a future event-sourced experience pipeline.
type Event struct {
	Type       string         `json:"type"`
	OwnerID    int64          `json:"owner_id"`
	WorkflowID int64          `json:"workflow_id"`
	RunID      int64          `json:"run_id"`
	NodeID     string         `json:"node_id"`
	Payload    map[string]any `json:"payload,omitempty"`
	OccurredAt time.Time      `json:"occurred_at"`
}
