package memory

import (
	"context"
	"time"
)

type Commander interface {
	Execute(ctx context.Context, request WriteRequest) (WriteResult, error)
	Revoke(ctx context.Context, ownerID, memoryID int64, reason string) error
	Supersede(ctx context.Context, ownerID, memoryID, replacementID int64, reason string) error
}

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
	ListByLevel(ctx context.Context, ownerID int64, level string, memoryTypes []string, limit int) ([]Memory, error)
	ListActiveOwnerIDs(ctx context.Context, limit int) ([]int64, error)
	IncrementUsageCount(ctx context.Context, ownerID int64, id int64) error
	IncrementPromotionCount(ctx context.Context, ownerID int64, id int64) error
	SoftDelete(ctx context.Context, ownerID, id int64) error
	MarkUsed(ctx context.Context, ownerID int64, ids []int64) error
	MarkExpired(ctx context.Context, ownerID int64, maxAgeDays int) (int64, error)
	UpdateDecayedImportance(ctx context.Context, ownerID int64, decayRate float64) (int64, error)
}

// ScopedReader is an optional read path for callers that know both the
// conversation and project. It keeps the legacy Repository contract intact
// while preventing project memories from being hidden behind a conversation
// filter or leaking across projects.
type ScopedReader interface {
	ListForReadScoped(ctx context.Context, ownerID, agentID int64, memoryTypes []string, conversationID, projectID *int64, limit int) ([]Memory, error)
}

// AtomicReplacementRepository is implemented by persistent repositories that
// can create a replacement, supersede the previous version and register both
// index changes in one transaction.
type AtomicReplacementRepository interface {
	Replace(ctx context.Context, ownerID, supersededID int64, replacement *Memory) error
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

// LifecycleRepository is the SQL-first lifecycle data surface used by
// usage-driven selection and pruning. Selection must be one deterministic
// query ordered by usage_count DESC, COALESCE(last_used_at, updated_at) DESC,
// id ASC with the cold-window and consolidated-protection filters pushed down;
// pruning soft-deletes explicit IDs. No scoring exists on this surface.
type LifecycleRepository interface {
	// SelectLifecycleCandidates returns active recallable rows ordered by
	// usage then recency, capped at limit. Rows whose recency
	// (COALESCE(last_used_at, updated_at)) falls before cutoff are included
	// only when their ID is protected. The lifecycle service re-applies the
	// same deterministic predicate over the returned rows.
	SelectLifecycleCandidates(ctx context.Context, ownerID int64, cutoff time.Time, protectedIDs []int64, limit int) ([]Memory, error)
	// ListColdRows returns active rows whose recency falls before cutoff,
	// ordered by id ASC. The SQL may push the protected-ID exclusion down;
	// the lifecycle service re-checks protection before deleting.
	ListColdRows(ctx context.Context, ownerID int64, cutoff time.Time, protectedIDs []int64) ([]Memory, error)
	// PruneMemories soft-deletes the given active rows and returns the IDs
	// that were actually deleted.
	PruneMemories(ctx context.Context, ownerID int64, ids []int64) ([]int64, error)
}
