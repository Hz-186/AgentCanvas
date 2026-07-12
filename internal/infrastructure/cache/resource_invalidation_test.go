package cache

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"agentcanvas/internal/domain/resource"
)

type fakeInvalidator struct {
	err   error
	calls int
}

func (f *fakeInvalidator) Invalidate(context.Context, int64, resource.Kind) error {
	f.calls++
	return f.err
}

type fakeInvalidationStore struct {
	events    []resource.InvalidationEvent
	enqueued  int
	processed int
}

func (s *fakeInvalidationStore) Enqueue(_ context.Context, ownerID int64, kind resource.Kind, cause error) error {
	s.enqueued++
	s.events = append(s.events, resource.InvalidationEvent{ID: int64(s.enqueued), OwnerID: ownerID, Kind: kind, LastError: cause.Error()})
	return nil
}
func (s *fakeInvalidationStore) ListPending(context.Context, int) ([]resource.InvalidationEvent, error) {
	return s.events, nil
}
func (s *fakeInvalidationStore) MarkProcessed(context.Context, int64) error {
	s.processed++
	s.events = nil
	return nil
}
func (s *fakeInvalidationStore) MarkFailed(context.Context, int64, int, time.Time, error) error {
	return nil
}
func (s *fakeInvalidationStore) DeleteProcessedBefore(context.Context, time.Time, int) (int64, error) {
	return 0, nil
}

func TestRetryingInvalidatorQueuesFailureAndRetries(t *testing.T) {
	next := &fakeInvalidator{err: errors.New("redis unavailable")}
	store := &fakeInvalidationStore{}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	invalidator := NewRetryingInvalidator(next, store, logger)

	if err := invalidator.Invalidate(context.Background(), 7, resource.KindKnowledgeBases); err != nil {
		t.Fatalf("Invalidate() error = %v", err)
	}
	if store.enqueued != 1 {
		t.Fatalf("expected one queued event, got %d", store.enqueued)
	}

	next.err = nil
	invalidator.process(context.Background())
	if store.processed != 1 || next.calls != 2 {
		t.Fatalf("expected successful retry, processed=%d calls=%d", store.processed, next.calls)
	}
}
