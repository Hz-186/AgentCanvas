package llm

import "context"

type ownerIDContextKey struct{}

func WithOwnerID(ctx context.Context, ownerID int64) context.Context {
	if ctx == nil || ownerID <= 0 {
		return ctx
	}
	return context.WithValue(ctx, ownerIDContextKey{}, ownerID)
}

func OwnerIDFromContext(ctx context.Context) (int64, bool) {
	if ctx == nil {
		return 0, false
	}
	ownerID, ok := ctx.Value(ownerIDContextKey{}).(int64)
	if !ok || ownerID <= 0 {
		return 0, false
	}
	return ownerID, true
}
