package cache

import (
	"context"
	"log/slog"
	"time"

	"agentcanvas/internal/domain/resource"
)

type RetryingInvalidator struct {
	next  resource.Invalidator
	store resource.InvalidationStore
	log   *slog.Logger
}

func NewRetryingInvalidator(next resource.Invalidator, store resource.InvalidationStore, log *slog.Logger) *RetryingInvalidator {
	return &RetryingInvalidator{next: next, store: store, log: log}
}

func (i *RetryingInvalidator) Invalidate(ctx context.Context, ownerID int64, kind resource.Kind) error {
	err := i.next.Invalidate(ctx, ownerID, kind)
	if err == nil {
		return nil
	}
	if enqueueErr := i.store.Enqueue(ctx, ownerID, kind, err); enqueueErr != nil {
		i.log.Error("cache invalidation enqueue failed", "kind", kind, "error", enqueueErr)
		return enqueueErr
	}
	i.log.Warn("cache invalidation queued for retry", "kind", kind, "error", err)
	return nil
}

func (i *RetryingInvalidator) Start(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		cleanupTicker := time.NewTicker(time.Hour)
		defer cleanupTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				i.process(ctx)
			case <-cleanupTicker.C:
				i.cleanup(ctx)
			}
		}
	}()
}

func (i *RetryingInvalidator) cleanup(ctx context.Context) {
	for {
		deleted, err := i.store.DeleteProcessedBefore(ctx, time.Now().UTC().Add(-24*time.Hour), 1000)
		if err != nil {
			i.log.Error("cache invalidation outbox cleanup failed", "error", err)
			return
		}
		if deleted < 1000 {
			return
		}
	}
}

func (i *RetryingInvalidator) process(ctx context.Context) {
	events, err := i.store.ListPending(ctx, 100)
	if err != nil {
		i.log.Error("cache invalidation outbox read failed", "error", err)
		return
	}
	for _, event := range events {
		if err := i.next.Invalidate(ctx, event.OwnerID, event.Kind); err != nil {
			attemptCount := event.AttemptCount + 1
			delay := time.Second << min(attemptCount, 8)
			_ = i.store.MarkFailed(ctx, event.ID, attemptCount, time.Now().UTC().Add(delay), err)
			continue
		}
		if err := i.store.MarkProcessed(ctx, event.ID); err != nil {
			i.log.Error("cache invalidation outbox completion failed", "event_id", event.ID, "error", err)
		}
	}
}
