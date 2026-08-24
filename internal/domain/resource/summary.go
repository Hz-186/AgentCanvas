package resource

import (
	"context"
	"time"

	"agentcanvas/internal/domain"
)

type Kind string

const (
	KindSkills         Kind = "skills"
	KindMemories       Kind = "memories"
	KindHTTPTools      Kind = "http-tools"
	KindKnowledgeBases Kind = "knowledge-bases"
)

func (k Kind) Valid() bool {
	switch k {
	case KindSkills, KindMemories, KindHTTPTools, KindKnowledgeBases:
		return true
	default:
		return false
	}
}

type Summary struct {
	ID            int64     `json:"id"`
	Name          string    `json:"name"`
	Description   string    `json:"description,omitempty"`
	Enabled       bool      `json:"enabled"`
	ResourceType  string    `json:"resource_type,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
	DocumentCount int       `json:"document_count,omitempty"`
	ChunkCount    int       `json:"chunk_count,omitempty"`
}

type Page struct {
	Items      []Summary `json:"items"`
	NextCursor string    `json:"next_cursor,omitempty"`
	HasMore    bool      `json:"has_more"`
}

type ListOptions struct {
	Limit  int
	Cursor string
}

type Query interface {
	List(ctx context.Context, ownerID int64, kind Kind, options ListOptions) (Page, error)
}

type Invalidator interface {
	Invalidate(ctx context.Context, ownerID int64, kind Kind) error
}

type InvalidationEvent struct {
	domain.BaseModel
	Kind         Kind       `gorm:"column:resource_kind"`
	AttemptCount int        `gorm:"column:attempt_count"`
	NextRetryAt  time.Time  `gorm:"column:next_retry_at"`
	ProcessedAt  *time.Time `gorm:"column:processed_at"`
	LastError    string     `gorm:"column:last_error"`
}

func (InvalidationEvent) TableName() string { return "cache_invalidation_outbox" }

type InvalidationStore interface {
	Enqueue(ctx context.Context, ownerID int64, kind Kind, cause error) error
	ListPending(ctx context.Context, limit int) ([]InvalidationEvent, error)
	MarkProcessed(ctx context.Context, id int64) error
	MarkFailed(ctx context.Context, id int64, attemptCount int, nextRetryAt time.Time, cause error) error
	DeleteProcessedBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error)
}
