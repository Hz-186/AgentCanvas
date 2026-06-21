package memory

import "context"

type Repository interface {
	Create(ctx context.Context, item *Memory) error
	Update(ctx context.Context, item *Memory) error
	FindByID(ctx context.Context, ownerID, id int64) (*Memory, error)
	List(ctx context.Context, ownerID int64, memoryTypes []string, conversationID *int64, limit, offset int) ([]Memory, error)
	ListForRead(ctx context.Context, ownerID int64, memoryTypes []string, conversationID *int64, limit int) ([]Memory, error)
	SoftDelete(ctx context.Context, ownerID, id int64) error
	MarkUsed(ctx context.Context, ownerID int64, ids []int64) error
}

type WriteLogRepository interface {
	Create(ctx context.Context, item *WriteLog) error
	ListByRun(ctx context.Context, ownerID, runID int64) ([]WriteLog, error)
}
