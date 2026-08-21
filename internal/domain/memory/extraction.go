package memory

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var ErrExtractionLeaseLost = errors.New("memory extraction worker lease lost")

type ExtractionStatus string

const (
	ExtractionPending   ExtractionStatus = "pending"
	ExtractionRunning   ExtractionStatus = "running"
	ExtractionCompleted ExtractionStatus = "completed"
	ExtractionFailed    ExtractionStatus = "failed"
)

type ExtractionJob struct {
	ID               int64           `json:"id" gorm:"primaryKey;column:id"`
	OwnerID          int64           `json:"owner_id" gorm:"column:owner_id"`
	ConversationID   int64           `json:"conversation_id" gorm:"column:conversation_id"`
	IdempotencyKey   string          `json:"idempotency_key" gorm:"column:idempotency_key"`
	TriggerReason    string          `json:"trigger_reason" gorm:"column:trigger_reason"`
	SourceMessageIDs json.RawMessage `json:"source_message_ids" gorm:"column:source_message_ids"`
	ThroughMessageID int64           `json:"through_message_id" gorm:"column:through_message_id"`
	Status           string          `json:"status" gorm:"column:status;default:pending"`
	DueAt            *time.Time      `json:"due_at" gorm:"column:due_at"`
	AttemptCount     int             `json:"attempt_count" gorm:"column:attempt_count"`
	LockedBy         string          `json:"locked_by" gorm:"column:locked_by"`
	LockedAt         *time.Time      `json:"locked_at" gorm:"column:locked_at"`
	LeaseExpiresAt   *time.Time      `json:"lease_expires_at" gorm:"column:lease_expires_at"`
	ResultJSON       json.RawMessage `json:"result_json" gorm:"column:result_json"`
	ErrorMessage     string          `json:"error_message" gorm:"column:error_message"`
	CreatedAt        time.Time       `json:"created_at" gorm:"column:created_at"`
	UpdatedAt        time.Time       `json:"updated_at" gorm:"column:updated_at"`
	CompletedAt      *time.Time      `json:"completed_at" gorm:"column:completed_at"`
}

func (ExtractionJob) TableName() string { return "memory_extraction_jobs" }

type MergeLog struct {
	ID         int64     `json:"id" gorm:"primaryKey;column:id"`
	OwnerID    int64     `json:"owner_id" gorm:"column:owner_id"`
	SourceID   int64     `json:"source_id" gorm:"column:source_id"`
	TargetID   int64     `json:"target_id" gorm:"column:target_id"`
	Similarity float64   `json:"similarity" gorm:"column:similarity"`
	Reason     string    `json:"reason" gorm:"column:reason"`
	CreatedAt  time.Time `json:"created_at" gorm:"column:created_at"`
}

func (MergeLog) TableName() string { return "memory_merge_logs" }

type ExtractionResult struct {
	ProfileMemories  []ExtractedMemoryItem `json:"profile_memories"`
	SummaryMemories  []ExtractedMemoryItem `json:"summary_memories"`
	EpisodicMemories []ExtractedMemoryItem `json:"episodic_memories"`
	TaskMemories     []ExtractedMemoryItem `json:"task_memories"`
}

type ExtractedMemoryItem struct {
	Title      string  `json:"title"`
	Content    string  `json:"content"`
	Importance float64 `json:"importance"`
	Confidence float64 `json:"confidence"`
}

type ExtractionJobRepository interface {
	Create(ctx context.Context, job *ExtractionJob) error
	Update(ctx context.Context, job *ExtractionJob) error
	FindByID(ctx context.Context, ownerID, id int64) (*ExtractionJob, error)
	FindByIdempotencyKey(ctx context.Context, ownerID int64, key string) (*ExtractionJob, error)
	ListByStatus(ctx context.Context, ownerID int64, status string, limit int) ([]ExtractionJob, error)
	ListPending(ctx context.Context, limit int) ([]ExtractionJob, error)
}

type ExtractionLeaseRepository interface {
	ClaimByID(ctx context.Context, ownerID, id int64, workerID string, leaseUntil time.Time) (*ExtractionJob, bool, error)
	RenewLease(ctx context.Context, id int64, workerID string, leaseUntil time.Time) error
	UpdateOwned(ctx context.Context, job *ExtractionJob, workerID string) error
}

type MergeLogRepository interface {
	Create(ctx context.Context, log *MergeLog) error
	ListByOwner(ctx context.Context, ownerID int64, limit int) ([]MergeLog, error)
}
