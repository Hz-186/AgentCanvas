package resource

import (
	"context"
	"time"
)

type Kind string

const (
	KindSkills         Kind = "skills"
	KindMemories       Kind = "memories"
	KindHTTPTools      Kind = "http-tools"
	KindWorkflows      Kind = "workflows"
	KindDialogs        Kind = "dialogs"
	KindKnowledgeBases Kind = "knowledge-bases"
)

func (k Kind) Valid() bool {
	switch k {
	case KindSkills, KindMemories, KindHTTPTools, KindWorkflows, KindDialogs, KindKnowledgeBases:
		return true
	default:
		return false
	}
}

type Summary struct {
	ID               int64     `json:"id"`
	Name             string    `json:"name"`
	Description      string    `json:"description,omitempty"`
	Status           int       `json:"status,omitempty"`
	ResourceType     string    `json:"resource_type,omitempty"`
	UpdatedAt        time.Time `json:"updated_at"`
	CurrentVersionID *int64    `json:"current_version_id,omitempty"`
	DocumentCount    int       `json:"document_count,omitempty"`
	ChunkCount       int       `json:"chunk_count,omitempty"`
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
	ID          int64      `gorm:"primaryKey;column:id"`
	OwnerID     int64      `gorm:"column:owner_id"`
	Kind        Kind       `gorm:"column:resource_kind"`
	Attempts    int        `gorm:"column:attempts"`
	NextRetryAt time.Time  `gorm:"column:next_retry_at"`
	ProcessedAt *time.Time `gorm:"column:processed_at"`
	LastError   string     `gorm:"column:last_error"`
	CreatedAt   time.Time  `gorm:"column:created_at"`
	UpdatedAt   time.Time  `gorm:"column:updated_at"`
}

func (InvalidationEvent) TableName() string { return "cache_invalidation_outbox" }

type InvalidationStore interface {
	Enqueue(ctx context.Context, ownerID int64, kind Kind, cause error) error
	ListPending(ctx context.Context, limit int) ([]InvalidationEvent, error)
	MarkProcessed(ctx context.Context, id int64) error
	MarkFailed(ctx context.Context, id int64, attempts int, nextRetryAt time.Time, cause error) error
	DeleteProcessedBefore(ctx context.Context, cutoff time.Time, limit int) (int64, error)
}
