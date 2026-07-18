package contextresource

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"math/rand"
	"time"

	"agentcanvas/internal/observability"
)

type Worker struct {
	Repository   Repository
	Index        Index
	WorkerID     string
	BatchSize    int
	Lease        time.Duration
	PollInterval time.Duration
	Logger       *slog.Logger
}

func (w *Worker) Run(ctx context.Context) {
	if w == nil || w.Repository == nil || w.Index == nil {
		return
	}
	if w.BatchSize <= 0 {
		w.BatchSize = 50
	}
	if w.Lease <= 0 {
		w.Lease = time.Minute
	}
	if w.PollInterval <= 0 {
		w.PollInterval = time.Second
	}
	for {
		processed, err := w.ProcessBatch(ctx)
		if err != nil {
			w.log("context resource index batch failed", "error", err)
		}
		if ctx.Err() != nil {
			return
		}
		if processed > 0 {
			continue
		}
		timer := time.NewTimer(w.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (w *Worker) ProcessBatch(ctx context.Context) (int, error) {
	items, err := w.Repository.Claim(ctx, w.WorkerID, w.BatchSize, w.Lease)
	if err != nil {
		return 0, err
	}
	var joined error
	for i := range items {
		if err := w.processWithLease(ctx, items[i]); err != nil {
			observability.ContextSystemMetrics.RecordOutbox(false)
			joined = errors.Join(joined, err)
		} else {
			observability.ContextSystemMetrics.RecordOutbox(true)
		}
	}
	return len(items), joined
}

func (w *Worker) processWithLease(ctx context.Context, item OutboxItem) error {
	if err := w.Repository.Renew(ctx, item.ID, w.WorkerID, w.Lease); err != nil {
		return err
	}
	itemCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	heartbeatErr := make(chan error, 1)
	go func() {
		interval := w.Lease / 3
		if interval < time.Second {
			interval = time.Second
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				heartbeatErr <- nil
				return
			case <-itemCtx.Done():
				heartbeatErr <- itemCtx.Err()
				return
			case <-ticker.C:
				if err := w.Repository.Renew(itemCtx, item.ID, w.WorkerID, w.Lease); err != nil {
					cancel()
					heartbeatErr <- err
					return
				}
			}
		}
	}()
	processErr := w.process(itemCtx, item)
	close(done)
	hbErr := <-heartbeatErr
	if processErr != nil {
		return processErr
	}
	_ = hbErr // a final heartbeat may observe the already-completed row
	return nil
}

func (w *Worker) process(ctx context.Context, item OutboxItem) error {
	profile := EmbeddingProfile{ProviderID: item.EmbeddingProviderID, Model: item.EmbeddingModel, Dimensions: item.EmbeddingDimensions, Hash: item.EmbeddingProfileHash}.Normalized()
	if item.Operation == OperationDelete {
		if err := w.Index.Delete(ctx, item); err != nil {
			return w.fail(ctx, item, err)
		}
	} else {
		document, err := w.Repository.LoadDocument(ctx, item)
		if err != nil {
			return w.fail(ctx, item, err)
		}
		if document == nil || document.ContentHash != item.ContentHash {
			// A newer version was committed. Completing this stale event is safe:
			// the newer outbox version owns the eventual index state.
			return w.Repository.Complete(ctx, item.ID, w.WorkerID, profile)
		}
		profile, err = w.Index.Upsert(ctx, *document, profile)
		if err != nil {
			return w.fail(ctx, item, err)
		}
	}
	return w.Repository.Complete(ctx, item.ID, w.WorkerID, profile)
}

func (w *Worker) fail(ctx context.Context, item OutboxItem, cause error) error {
	attempt := item.AttemptCount + 1
	delay := time.Duration(math.Min(math.Pow(2, float64(attempt)), 300)) * time.Second
	delay += time.Duration(rand.Intn(1000)) * time.Millisecond //nolint:gosec // backoff jitter only
	if err := w.Repository.Retry(ctx, item.ID, w.WorkerID, cause, time.Now().UTC().Add(delay)); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func (w *Worker) log(message string, args ...any) {
	if w.Logger != nil {
		w.Logger.Warn(message, args...)
	}
}
