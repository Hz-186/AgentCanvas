package redis

import (
	"context"
	"testing"
	"time"

	"agentcanvas/internal/domain/memory"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newTestWMRepo(t *testing.T) (*WorkingMemoryRepository, *miniredis.Miniredis, func()) {
	t.Helper()
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	repo := NewWorkingMemoryRepository(client)
	return repo, mr, func() { client.Close(); mr.Close() }
}

func TestWorkingMemoryRepo_GetNil(t *testing.T) {
	repo, _, cleanup := newTestWMRepo(t)
	defer cleanup()

	got, err := repo.Get(context.Background(), 100, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatal("expected nil for non-existent working memory")
	}
}

func TestWorkingMemoryRepo_SaveAndGet(t *testing.T) {
	repo, _, cleanup := newTestWMRepo(t)
	defer cleanup()
	ctx := context.Background()

	wm := &memory.WorkingMemory{
		OwnerID:        100,
		ConversationID: 1,
		ActiveTask:     &memory.WorkingTask{Goal: "test goal", CurrentStep: "step 1"},
		RecentFacts:    []memory.WorkingFact{{Fact: "user likes Go", Confidence: 0.9}},
		RoundNumber:    3,
	}

	if err := repo.Save(ctx, wm); err != nil {
		t.Fatal(err)
	}

	got, err := repo.Get(ctx, 100, 1)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected working memory to exist")
	}
	if got.ActiveTask.Goal != "test goal" {
		t.Fatalf("unexpected goal: %s", got.ActiveTask.Goal)
	}
	if got.RoundNumber != 3 {
		t.Fatalf("unexpected round: %d", got.RoundNumber)
	}
}

func TestWorkingMemoryRepo_Delete(t *testing.T) {
	repo, _, cleanup := newTestWMRepo(t)
	defer cleanup()
	ctx := context.Background()

	wm := &memory.WorkingMemory{OwnerID: 100, ConversationID: 1}
	repo.Save(ctx, wm)

	if err := repo.Delete(ctx, 100, 1); err != nil {
		t.Fatal(err)
	}

	got, _ := repo.Get(ctx, 100, 1)
	if got != nil {
		t.Fatal("expected nil after delete")
	}
}

func TestWorkingMemoryRepo_SaveUpdatesLastUpdated(t *testing.T) {
	repo, _, cleanup := newTestWMRepo(t)
	defer cleanup()
	ctx := context.Background()

	wm := &memory.WorkingMemory{OwnerID: 100, ConversationID: 1}
	repo.Save(ctx, wm)

	got, _ := repo.Get(ctx, 100, 1)
	if got.LastUpdated.IsZero() {
		t.Fatal("expected non-zero last_updated")
	}

	old := got.LastUpdated
	time.Sleep(10 * time.Millisecond)
	repo.Save(ctx, got)

	updated, _ := repo.Get(ctx, 100, 1)
	if !updated.LastUpdated.After(old) {
		t.Fatal("expected last_updated to be updated")
	}
}

func TestWorkingMemoryRepo_TTL(t *testing.T) {
	repo, mr, cleanup := newTestWMRepo(t)
	defer cleanup()
	ctx := context.Background()

	wm := &memory.WorkingMemory{OwnerID: 100, ConversationID: 1}
	repo.Save(ctx, wm)

	mr.FastForward(25 * time.Hour)
	got, _ := repo.Get(ctx, 100, 1)
	if got != nil {
		t.Fatal("expected nil after TTL expiration")
	}
}

func TestWorkingMemoryRepo_DifferentConversations(t *testing.T) {
	repo, _, cleanup := newTestWMRepo(t)
	defer cleanup()
	ctx := context.Background()

	wm1 := &memory.WorkingMemory{OwnerID: 100, ConversationID: 1, RoundNumber: 1}
	wm2 := &memory.WorkingMemory{OwnerID: 100, ConversationID: 2, RoundNumber: 5}
	repo.Save(ctx, wm1)
	repo.Save(ctx, wm2)

	got1, _ := repo.Get(ctx, 100, 1)
	got2, _ := repo.Get(ctx, 100, 2)

	if got1.RoundNumber != 1 {
		t.Fatalf("unexpected round for conv 1: %d", got1.RoundNumber)
	}
	if got2.RoundNumber != 5 {
		t.Fatalf("unexpected round for conv 2: %d", got2.RoundNumber)
	}
}
