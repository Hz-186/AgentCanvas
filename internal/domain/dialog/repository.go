package dialog

import "context"

type Repository interface {
	Create(ctx context.Context, item *Dialog) error
	ListByOwner(ctx context.Context, ownerID int64) ([]Dialog, error)
	FindByID(ctx context.Context, ownerID, id int64) (*Dialog, error)
	Update(ctx context.Context, item *Dialog) error
	SoftDelete(ctx context.Context, ownerID, id int64) error
}
