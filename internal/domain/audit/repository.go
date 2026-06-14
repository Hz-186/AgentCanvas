package audit

import "context"

type Repository interface {
	Create(ctx context.Context, log *Log) error
	ListByOwner(ctx context.Context, ownerID int64, limit, offset int) ([]Log, error)
}
