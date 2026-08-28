package reflection

import (
	"encoding/json"
	"time"

	"agentcanvas/internal/domain"
)

const (
	ScopeAgent = "agent"

	KindErrorLesson       = "error_lesson"
	KindImportantStrategy = "important_strategy"

	StatusValidated  = "validated"
	StatusDisputed   = "disputed"
	StatusSuperseded = "superseded"
	StatusArchived   = "archived"
)

// Reflection is an evidence-backed policy lesson derived from an Agent trajectory.
// It is separate from factual/user memory so provenance and usefulness can evolve independently.
type Reflection struct {
	domain.SoftDeleteModel
	AgentID             int64           `json:"agent_id" gorm:"column:agent_id"`
	SourceRunID         int64           `json:"source_run_id" gorm:"column:source_run_id"`
	SupersedesID        *int64          `json:"supersedes_id,omitempty" gorm:"column:supersedes_id"`
	Scope               string          `json:"scope" gorm:"column:scope"`
	Kind                string          `json:"kind" gorm:"column:kind"`
	Status              string          `json:"status" gorm:"column:status"`
	Mode                string          `json:"mode" gorm:"column:mode"`
	TriggerType         string          `json:"trigger_type" gorm:"column:trigger_type"`
	TaskFingerprint     string          `json:"task_fingerprint" gorm:"column:task_fingerprint"`
	TaskSummary         string          `json:"task_summary" gorm:"column:task_summary"`
	RootCauseCategory   string          `json:"root_cause_category" gorm:"column:root_cause_category"`
	RootCause           string          `json:"root_cause" gorm:"column:root_cause"`
	CorrectiveAction    string          `json:"corrective_action" gorm:"column:corrective_action"`
	Lesson              string          `json:"lesson" gorm:"column:lesson"`
	Applicability       string          `json:"applicability" gorm:"column:applicability"`
	EvidenceJSON        json.RawMessage `json:"evidence_json" gorm:"column:evidence_json"`
	TagsJSON            json.RawMessage `json:"tags_json" gorm:"column:tags_json"`
	EmbeddingProviderID int64           `json:"embedding_provider_id,omitempty" gorm:"column:embedding_provider_id"`
	EmbeddingModel      string          `json:"embedding_model,omitempty" gorm:"column:embedding_model"`
	EmbeddingDimensions int             `json:"embedding_dimensions,omitempty" gorm:"column:embedding_dimensions"`
	Importance          float64         `json:"importance" gorm:"column:importance"`
	Confidence          float64         `json:"confidence" gorm:"column:confidence"`
	ContentHash         string          `json:"content_hash" gorm:"column:content_hash"`
	RecallCount         int             `json:"recall_count" gorm:"column:recall_count"`
	SuccessfulUseCount  int             `json:"successful_use_count" gorm:"column:successful_use_count"`
	HarmfulCount        int             `json:"harmful_count" gorm:"column:harmful_count"`
	LastRecalledAt      *time.Time      `json:"last_recalled_at,omitempty" gorm:"column:last_recalled_at"`
	ExpiresAt           *time.Time      `json:"expires_at,omitempty" gorm:"column:expires_at"`
}

func (Reflection) TableName() string { return "agent_reflections" }
