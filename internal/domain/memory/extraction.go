package memory

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"agentcanvas/internal/domain"
)

var ErrExtractionLeaseLost = errors.New("memory extraction worker lease lost")

type ExtractionStatus string

const (
	ExtractionPending    ExtractionStatus = "pending"
	ExtractionRunning    ExtractionStatus = "running"
	ExtractionCompleted  ExtractionStatus = "completed"
	ExtractionFailed     ExtractionStatus = "failed"
	ExtractionSuperseded ExtractionStatus = "superseded"
)

type ExtractionJob struct {
	domain.BaseModel
	ConversationID     int64           `json:"conversation_id" gorm:"column:conversation_id"`
	ProjectID          int64           `json:"project_id,omitempty" gorm:"column:project_id"`
	IdempotencyKey     string          `json:"idempotency_key" gorm:"column:idempotency_key"`
	TriggerReason      string          `json:"trigger_reason" gorm:"column:trigger_reason"`
	SourceMessageIDs   json.RawMessage `json:"source_message_ids" gorm:"column:source_message_ids"`
	ThroughMessageID   int64           `json:"through_message_id" gorm:"column:through_message_id"`
	Status             string          `json:"status" gorm:"column:status;default:pending"`
	DueAt              *time.Time      `json:"due_at" gorm:"column:due_at"`
	AttemptCount       int             `json:"attempt_count" gorm:"column:attempt_count"`
	Phase2AttemptCount int             `json:"phase2_attempt_count" gorm:"column:phase2_attempt_count"`
	LockedBy           string          `json:"locked_by" gorm:"column:locked_by"`
	LockedAt           *time.Time      `json:"locked_at" gorm:"column:locked_at"`
	LeaseExpiresAt     *time.Time      `json:"lease_expires_at" gorm:"column:lease_expires_at"`
	ResultJSON         json.RawMessage `json:"result_json" gorm:"column:result_json"`
	ErrorMessage       string          `json:"error_message" gorm:"column:error_message"`
	CompletedAt        *time.Time      `json:"completed_at" gorm:"column:completed_at"`
}

func (ExtractionJob) TableName() string { return "memory_extraction_jobs" }

type ExtractionResult struct {
	ProfileMemories  []ExtractedMemoryItem `json:"profile_memories"`
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
