package knowledge

import (
	"context"
	"time"
)

type BaseRepository interface {
	Create(ctx context.Context, kb *KnowledgeBase) error
	ListByOwner(ctx context.Context, ownerID int64) ([]KnowledgeBase, error)
	FindByID(ctx context.Context, ownerID, id int64) (*KnowledgeBase, error)
	Update(ctx context.Context, kb *KnowledgeBase) error
	SoftDelete(ctx context.Context, ownerID, id int64) error
	AdjustCounts(ctx context.Context, ownerID, id int64, documentDelta, chunkDelta int) error
}

type KnowledgeBaseRepository = BaseRepository

type DocumentRepository interface {
	Create(ctx context.Context, doc *Document) error
	ListByKnowledgeBase(ctx context.Context, ownerID, knowledgeBaseID int64) ([]Document, error)
	FindByID(ctx context.Context, ownerID, id int64) (*Document, error)
	Update(ctx context.Context, doc *Document) error
	SetEnabled(ctx context.Context, ownerID, id int64, enabled bool) error
	SoftDelete(ctx context.Context, ownerID, id int64) error
	SoftDeleteByKnowledgeBase(ctx context.Context, ownerID, knowledgeBaseID int64) error
}

type ChunkRepository interface {
	CreateBatch(ctx context.Context, chunks []DocumentChunk) error
	ListByDocument(ctx context.Context, ownerID, documentID int64) ([]DocumentChunk, error)
	DeleteByDocument(ctx context.Context, ownerID, documentID int64) error
	DeleteByKnowledgeBase(ctx context.Context, ownerID, knowledgeBaseID int64) error
}

type IngestionJobRepository interface {
	Create(ctx context.Context, job *IngestionJob) error
	FindByID(ctx context.Context, ownerID, id int64) (*IngestionJob, error)
	ClaimNext(ctx context.Context, workerID string) (*IngestionJob, error)
	ClaimByID(ctx context.Context, ownerID, id int64, workerID string) (*IngestionJob, bool, error)
	MarkCompleted(ctx context.Context, id int64) error
	MarkFailed(ctx context.Context, id int64, message string) (bool, error)
}

type ReliableIngestionJobRepository interface {
	RenewLock(ctx context.Context, id int64, workerID string, lockedAt time.Time) error
	MarkCompletedOwned(ctx context.Context, id int64, workerID string) error
	MarkFailedOwned(ctx context.Context, id int64, workerID, message string) (bool, error)
}

// RetryableIngestionJobRepository persists the next retry time in the
// business row instead of relying on an in-memory transport delay.
type RetryableIngestionJobRepository interface {
	MarkFailedAt(ctx context.Context, id int64, message string, retryAt time.Time) (bool, error)
	MarkFailedOwnedAt(ctx context.Context, id int64, workerID, message string, retryAt time.Time) (bool, error)
}

type GenerationCommitter interface {
	Activate(ctx context.Context, doc *Document, cleanup *IngestionJob, chunkDelta int) error
}

type RetrievalLogRepository interface {
	Create(ctx context.Context, log *RetrievalLog) error
}
