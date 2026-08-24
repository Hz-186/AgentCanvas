package knowledge

import (
	"errors"
	"time"

	"agentcanvas/internal/domain"
)

var ErrIngestionLeaseLost = errors.New("ingestion worker lease lost")

const (
	IngestionJobTypeDocument          = "document_ingestion"
	IngestionJobTypeGenerationCleanup = "document_generation_cleanup"

	IngestionJobStatusPending    = "pending"
	IngestionJobStatusProcessing = "processing"
	IngestionJobStatusCompleted  = "completed"
	IngestionJobStatusFailed     = "failed"
)

type IngestionJob struct {
	domain.BaseModel
	KnowledgeBaseID int64      `json:"knowledge_base_id" gorm:"column:knowledge_base_id"`
	DocumentID      int64      `json:"document_id" gorm:"column:document_id"`
	JobType         string     `json:"job_type" gorm:"column:job_type"`
	Status          string     `json:"status" gorm:"column:status"`
	Priority        int        `json:"priority" gorm:"column:priority"`
	AttemptCount    int        `json:"attempt_count" gorm:"column:attempt_count"`
	MaxAttempts     int        `json:"max_attempts" gorm:"column:max_attempts"`
	RetryAt         *time.Time `json:"retry_at,omitempty" gorm:"column:retry_at"`
	ErrorMessage    string     `json:"error_message,omitempty" gorm:"column:error_message"`
	LockedBy        string     `json:"locked_by" gorm:"column:locked_by"`
	LockedAt        *time.Time `json:"locked_at" gorm:"column:locked_at"`
	StartedAt       *time.Time `json:"started_at" gorm:"column:started_at"`
	FinishedAt      *time.Time `json:"finished_at" gorm:"column:finished_at"`
}

func (IngestionJob) TableName() string { return "ingestion_jobs" }
