package memory

import "context"

type CandidateRequest struct {
	OwnerID              int64
	AgentID              int64
	ConversationID       int64
	ProjectID            int64
	SourceConversationID int64
	SourceProjectID      int64
	RunID                int64
	SourceID             string
	MemoryID             int64
	MemoryType           string
	Title                string
	Content              string
	Action               string
	Importance           float64
	Evidence             []string
	Source               string
	ScopeType            string
	ScopeID              int64
}

type CandidateWriter interface {
	Suggest(ctx context.Context, request CandidateRequest) (int64, error)
}

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
	ListByLevel(ctx context.Context, ownerID int64, level string, memoryTypes []string, limit int) ([]Memory, error)
	ListActiveOwnerIDs(ctx context.Context, limit int) ([]int64, error)
	IncrementRecallCount(ctx context.Context, ownerID int64, id int64) error
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

type WriteLogRepository interface {
	Create(ctx context.Context, item *WriteLog) error
	ListByRun(ctx context.Context, ownerID, runID int64) ([]WriteLog, error)
}

type RecallLogRepository interface {
	Create(ctx context.Context, item *RecallLog) error
	List(ctx context.Context, ownerID, memoryID int64, limit int) ([]RecallLog, error)
	SetFeedback(ctx context.Context, ownerID, id int64, feedback string) error
}
