package memory_usecase

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/observability"

	miniredis "github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestConsolidationService_UpgradeShortTerm(t *testing.T) {
	repo := &fakeMemRepo{items: map[int64]*memory.Memory{}}
	repo.Create(context.Background(), &memory.Memory{
		OwnerID: 100, MemoryType: memory.TypeProfile, MemoryLevel: memory.LevelShortTerm,
		Importance: 0.8, AccessCount: 5, Content: "high importance and accessed",
	})
	repo.Create(context.Background(), &memory.Memory{
		OwnerID: 100, MemoryType: memory.TypeProfile, MemoryLevel: memory.LevelShortTerm,
		Importance: 0.7, AccessCount: 1, Content: "low access",
	})

	svc := NewConsolidationService(repo)
	upgraded, err := svc.UpgradeShortTermToLongTerm(context.Background(), 100, 3, 0.6)
	if err != nil {
		t.Fatal(err)
	}
	if upgraded != 1 {
		t.Fatalf("expected 1 upgraded, got %d", upgraded)
	}

	items, _ := repo.List(context.Background(), 100, nil, nil, 50, 0)
	for _, item := range items {
		if item.Content == "high importance and accessed" && item.MemoryLevel != memory.LevelLongTerm {
			t.Fatalf("expected long_term for upgraded memory, got %s", item.MemoryLevel)
		}
	}
}

func TestConsolidationService_DowngradeWeak(t *testing.T) {
	repo := &fakeMemRepo{items: map[int64]*memory.Memory{}}
	repo.Create(context.Background(), &memory.Memory{
		OwnerID: 100, MemoryType: memory.TypeProfile, MemoryLevel: memory.LevelLongTerm,
		Importance: 0.1, AccessCount: 1, Content: "low importance long term",
	})
	repo.Create(context.Background(), &memory.Memory{
		OwnerID: 100, MemoryType: memory.TypeProfile, MemoryLevel: memory.LevelLongTerm,
		Importance: 0.9, AccessCount: 10, Content: "high importance long term",
	})

	svc := NewConsolidationService(repo)
	downgraded, err := svc.DowngradeWeakLongTerm(context.Background(), 100, 0.15)
	if err != nil {
		t.Fatal(err)
	}
	if downgraded != 1 {
		t.Fatalf("expected 1 downgraded, got %d", downgraded)
	}
}

func TestConsolidationService_RunFullCycle(t *testing.T) {
	repo := &fakeMemRepo{items: map[int64]*memory.Memory{}}
	repo.Create(context.Background(), &memory.Memory{
		OwnerID: 100, MemoryType: memory.TypeProfile, MemoryLevel: memory.LevelShortTerm,
		Importance: 0.7, AccessCount: 4, Content: "should upgrade",
	})
	repo.Create(context.Background(), &memory.Memory{
		OwnerID: 100, MemoryType: memory.TypeProfile, MemoryLevel: memory.LevelLongTerm,
		Importance: 0.1, AccessCount: 1, Content: "should downgrade",
	})

	svc := NewConsolidationService(repo)
	result, err := svc.RunConsolidationCycle(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}

	if result.Upgraded != 1 {
		t.Fatalf("expected 1 upgraded, got %d", result.Upgraded)
	}
	if result.Downgraded != 1 {
		t.Fatalf("expected 1 downgraded, got %d", result.Downgraded)
	}
}

type blockingOwnerRepository struct {
	memory.Repository
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (r *blockingOwnerRepository) ListActiveOwnerIDs(ctx context.Context, _ int) ([]int64, error) {
	if r.calls.Add(1) == 1 {
		close(r.entered)
	}
	select {
	case <-r.release:
		return nil, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestSchedulerDistributedLockAllowsOnlyOneInstance(t *testing.T) {
	server := miniredis.RunT(t)
	redisClient := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = redisClient.Close() })
	repository := &blockingOwnerRepository{entered: make(chan struct{}), release: make(chan struct{})}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	first := NewScheduler(repository, redisClient, time.Hour, logger)
	second := NewScheduler(repository, redisClient, time.Hour, logger)

	done := make(chan struct{})
	go func() {
		defer close(done)
		first.runOnce(context.Background())
	}()
	select {
	case <-repository.entered:
	case <-time.After(time.Second):
		t.Fatal("first scheduler did not enter the cycle")
	}
	second.runOnce(context.Background())
	close(repository.release)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("first scheduler did not finish")
	}
	if calls := repository.calls.Load(); calls != 1 {
		t.Fatalf("scheduler cycle calls = %d, want 1", calls)
	}
}

type failingOwnerRepository struct{ memory.Repository }

func (failingOwnerRepository) ListActiveOwnerIDs(context.Context, int) ([]int64, error) {
	return nil, errors.New("owners unavailable")
}

func TestSchedulerFailureIsExposedByMemoryMetrics(t *testing.T) {
	before := observability.MemoryRuntimeMetrics.Snapshot()
	scheduler := NewScheduler(failingOwnerRepository{}, nil, time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
	scheduler.runOnce(context.Background())
	after := observability.MemoryRuntimeMetrics.Snapshot()
	if after["scheduler_runs"] != before["scheduler_runs"]+1 || after["scheduler_failures"] != before["scheduler_failures"]+1 {
		t.Fatalf("scheduler metrics before=%v after=%v", before, after)
	}
}
