package memory

import (
	"context"
)

type Repository interface {
	Create(ctx context.Context, item *Memory) error
	Update(ctx context.Context, item *Memory) error
	FindByID(ctx context.Context, ownerID, id int64) (*Memory, error)
	FindByIDs(ctx context.Context, ownerID int64, ids []int64) ([]Memory, error)
	List(ctx context.Context, ownerID int64, memoryTypes []string, conversationID *int64, limit, offset int) ([]Memory, error)
	ListForRead(ctx context.Context, ownerID int64, memoryTypes []string, conversationID *int64, limit int) ([]Memory, error)
	// ListBySources returns active rows for the given production sources (for
	// example ad_hoc and extraction) so consolidation evidence is always a
	// memory row. Source refs stored in artifacts MUST reference the returned
	// rows by their memories.id — never a job id.
	ListBySources(ctx context.Context, ownerID int64, sources []string, limit int) ([]Memory, error)
	MarkUsed(ctx context.Context, ownerID int64, ids []int64) error
}

// ScopedReader is an optional read path for callers that know both the
// conversation and project. It keeps the legacy Repository contract intact
// while preventing project memories from being hidden behind a conversation
// filter or leaking across projects.
type ScopedReader interface {
	ListForReadScoped(ctx context.Context, ownerID, agentID int64, memoryTypes []string, conversationID, projectID *int64, limit int) ([]Memory, error)
}

type ListFilter struct {
	MemoryTypes          []string
	SourceConversationID *int64
	SourceProjectID      *int64
	Statuses             []string
	ScopeTypes           []string
	ScopeID              *int64
	Sources              []string
	Limit                int
	Offset               int
}

type FilteredRepository interface {
	ListFiltered(ctx context.Context, ownerID int64, filter ListFilter) ([]Memory, error)
}

type RecallLogRepository interface {
	Create(ctx context.Context, item *RecallLog) error
	List(ctx context.Context, ownerID, memoryID int64, limit int) ([]RecallLog, error)
	SetFeedback(ctx context.Context, ownerID, id int64, feedback string) error
}

