package memory

import "context"

type SemanticRetriever interface {
	Index(ctx context.Context, item Memory) error
	Search(ctx context.Context, ownerID int64, query string, memoryTypes []string, limit int) ([]int64, error)
	Delete(ctx context.Context, memoryID int64) error
}
