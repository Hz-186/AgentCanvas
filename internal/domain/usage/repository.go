package usage

import "context"

type Repository interface {
	Create(ctx context.Context, log *ModelUsageLog) error
}
