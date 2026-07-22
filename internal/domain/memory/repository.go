package memory

import "context"

type Repository interface {
	Create(ctx context.Context, item *Memory) error
	Update(ctx context.Context, item *Memory) error
	FindByID(ctx context.Context, ownerID, id int64) (*Memory, error)
	FindByIDs(ctx context.Context, ownerID int64, ids []int64) ([]Memory, error)
	List(ctx context.Context, ownerID int64, memoryTypes []string, conversationID *int64, limit, offset int) ([]Memory, error)
	ListForRead(ctx context.Context, ownerID int64, memoryTypes []string, conversationID *int64, limit int) ([]Memory, error)
	ListByLevel(ctx context.Context, ownerID int64, level string, memoryTypes []string, limit int) ([]Memory, error)
	ListActiveOwnerIDs(ctx context.Context, limit int) ([]int64, error)
	IncrementAccessCount(ctx context.Context, ownerID int64, id int64) error
	IncrementConsolidationCount(ctx context.Context, ownerID int64, id int64) error
	SoftDelete(ctx context.Context, ownerID, id int64) error
	MarkUsed(ctx context.Context, ownerID int64, ids []int64) error
	MarkExpired(ctx context.Context, ownerID int64, maxAgeDays int) (int64, error)
	UpdateDecayedImportance(ctx context.Context, ownerID int64, decayRate float64) (int64, error)
	SetEmbedding(ctx context.Context, ownerID, id int64, embedding []byte) error
}

type WriteLogRepository interface {
	Create(ctx context.Context, item *WriteLog) error
	ListByRun(ctx context.Context, ownerID, runID int64) ([]WriteLog, error)
}
