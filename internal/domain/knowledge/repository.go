package knowledge

import "context"

type KnowledgeBaseRepository interface {
	Create(ctx context.Context, kb *KnowledgeBase) error
	ListByOwner(ctx context.Context, ownerID int64) ([]KnowledgeBase, error)
	FindByID(ctx context.Context, ownerID, id int64) (*KnowledgeBase, error)
	Update(ctx context.Context, kb *KnowledgeBase) error
	SoftDelete(ctx context.Context, ownerID, id int64) error
	AdjustCounts(ctx context.Context, ownerID, id int64, documentDelta, chunkDelta int) error
}

type DocumentRepository interface {
	Create(ctx context.Context, doc *Document) error
	ListByKnowledgeBase(ctx context.Context, ownerID, kbID int64) ([]Document, error)
	FindByID(ctx context.Context, ownerID, id int64) (*Document, error)
	Update(ctx context.Context, doc *Document) error
	SoftDelete(ctx context.Context, ownerID, id int64) error
	SoftDeleteByKnowledgeBase(ctx context.Context, ownerID, kbID int64) error
}

type ChunkRepository interface {
	CreateBatch(ctx context.Context, chunks []DocumentChunk) error
	ListByDocument(ctx context.Context, ownerID, documentID int64) ([]DocumentChunk, error)
	UpdateIndexRefs(ctx context.Context, chunks []DocumentChunk) error
	DeleteByDocument(ctx context.Context, ownerID, documentID int64) error
	DeleteByKnowledgeBase(ctx context.Context, ownerID, kbID int64) error
}

type IngestionJobRepository interface {
	Create(ctx context.Context, job *IngestionJob) error
	FindByID(ctx context.Context, ownerID, id int64) (*IngestionJob, error)
	ClaimNext(ctx context.Context, workerID string) (*IngestionJob, error)
	MarkCompleted(ctx context.Context, id int64) error
	MarkFailed(ctx context.Context, id int64, message string) error
}

type RetrievalLogRepository interface {
	Create(ctx context.Context, log *RetrievalLog) error
}
