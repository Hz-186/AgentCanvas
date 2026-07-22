package job

import (
	"context"
	"sync"
	"testing"
	"time"

	"agentcanvas/internal/domain/memory"
)

type fakeSchedulerMemoryRepo struct {
	owners []int64
}

func (r *fakeSchedulerMemoryRepo) Create(ctx context.Context, item *memory.Memory) error { return nil }
func (r *fakeSchedulerMemoryRepo) Update(ctx context.Context, item *memory.Memory) error { return nil }
func (r *fakeSchedulerMemoryRepo) FindByID(ctx context.Context, ownerID, id int64) (*memory.Memory, error) {
	return nil, nil
}
func (r *fakeSchedulerMemoryRepo) FindByIDs(context.Context, int64, []int64) ([]memory.Memory, error) {
	return nil, nil
}
func (r *fakeSchedulerMemoryRepo) List(ctx context.Context, ownerID int64, memoryTypes []string, conversationID *int64, limit, offset int) ([]memory.Memory, error) {
	return nil, nil
}
func (r *fakeSchedulerMemoryRepo) ListForRead(ctx context.Context, ownerID int64, memoryTypes []string, conversationID *int64, limit int) ([]memory.Memory, error) {
	return nil, nil
}
func (r *fakeSchedulerMemoryRepo) ListByLevel(ctx context.Context, ownerID int64, level string, memoryTypes []string, limit int) ([]memory.Memory, error) {
	return nil, nil
}
func (r *fakeSchedulerMemoryRepo) ListActiveOwnerIDs(ctx context.Context, limit int) ([]int64, error) {
	return append([]int64(nil), r.owners...), nil
}
func (r *fakeSchedulerMemoryRepo) IncrementAccessCount(ctx context.Context, ownerID int64, id int64) error {
	return nil
}
func (r *fakeSchedulerMemoryRepo) IncrementConsolidationCount(ctx context.Context, ownerID int64, id int64) error {
	return nil
}
func (r *fakeSchedulerMemoryRepo) SoftDelete(ctx context.Context, ownerID, id int64) error {
	return nil
}
func (r *fakeSchedulerMemoryRepo) MarkUsed(ctx context.Context, ownerID int64, ids []int64) error {
	return nil
}
func (r *fakeSchedulerMemoryRepo) MarkExpired(ctx context.Context, ownerID int64, maxAgeDays int) (int64, error) {
	return 0, nil
}
func (r *fakeSchedulerMemoryRepo) UpdateDecayedImportance(ctx context.Context, ownerID int64, decayRate float64) (int64, error) {
	return 0, nil
}
func (r *fakeSchedulerMemoryRepo) SetEmbedding(ctx context.Context, ownerID, id int64, embedding []byte) error {
	return nil
}

func TestMemorySchedulerStopIsIdempotent(t *testing.T) {
	scheduler := NewMemoryScheduler(&fakeSchedulerMemoryRepo{}, MemorySchedulerConfig{ConsolidationInterval: time.Hour})
	scheduler.Start(context.Background())

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			scheduler.Stop()
		}()
	}
	wg.Wait()
}

func TestMemorySchedulerGetsOwnersViaRepository(t *testing.T) {
	repo := &fakeSchedulerMemoryRepo{owners: []int64{10, 20}}
	scheduler := NewMemoryScheduler(repo, MemorySchedulerConfig{ConsolidationInterval: time.Hour})

	owners, err := scheduler.getActiveOwners(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(owners) != 2 || owners[0] != 10 || owners[1] != 20 {
		t.Fatalf("unexpected owners: %v", owners)
	}
}
