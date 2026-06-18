package knowledge

import "time"

const (
	IngestionJobTypeDocument = "document_ingestion"

	IngestionJobStatusPending    = "pending"
	IngestionJobStatusProcessing = "processing"
	IngestionJobStatusCompleted  = "completed"
	IngestionJobStatusFailed     = "failed"
)

type IngestionJob struct {
	ID           int64      `json:"id" gorm:"primaryKey;column:id"`
	OwnerID      int64      `json:"owner_id" gorm:"column:owner_id"`
	KBID         int64      `json:"kb_id" gorm:"column:kb_id"`
	DocumentID   int64      `json:"document_id" gorm:"column:document_id"`
	JobType      string     `json:"job_type" gorm:"column:job_type"`
	Status       string     `json:"status" gorm:"column:status"`
	Priority     int        `json:"priority" gorm:"column:priority"`
	AttemptCount int        `json:"attempt_count" gorm:"column:attempt_count"`
	MaxAttempts  int        `json:"max_attempts" gorm:"column:max_attempts"`
	ErrorMessage string     `json:"error_message,omitempty" gorm:"column:error_message"`
	LockedBy     string     `json:"locked_by" gorm:"column:locked_by"`
	LockedAt     *time.Time `json:"locked_at" gorm:"column:locked_at"`
	StartedAt    *time.Time `json:"started_at" gorm:"column:started_at"`
	FinishedAt   *time.Time `json:"finished_at" gorm:"column:finished_at"`
	CreatedAt    time.Time  `json:"created_at" gorm:"column:created_at"`
	UpdatedAt    time.Time  `json:"updated_at" gorm:"column:updated_at"`
}

func (IngestionJob) TableName() string { return "ingestion_jobs" }
