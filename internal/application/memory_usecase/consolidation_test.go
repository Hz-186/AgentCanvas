package memory_usecase

import (
	"context"
	"testing"

	"agentcanvas/internal/domain/memory"
)

func TestConsolidationService_UpgradeShortTerm(t *testing.T) {
	repo := &fakeMemRepo{items: map[int64]*memory.Memory{}}
	repo.Create(context.Background(), &memory.Memory{
		OwnerID:   100, MemoryType: memory.TypeProfile, MemoryLevel: memory.LevelShortTerm,
		Importance: 0.8, AccessCount: 5, Content: "high importance and accessed",
	})
	repo.Create(context.Background(), &memory.Memory{
		OwnerID:   100, MemoryType: memory.TypeProfile, MemoryLevel: memory.LevelShortTerm,
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
		OwnerID:   100, MemoryType: memory.TypeProfile, MemoryLevel: memory.LevelLongTerm,
		Importance: 0.1, AccessCount: 1, Content: "low importance long term",
	})
	repo.Create(context.Background(), &memory.Memory{
		OwnerID:   100, MemoryType: memory.TypeProfile, MemoryLevel: memory.LevelLongTerm,
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
		OwnerID:   100, MemoryType: memory.TypeProfile, MemoryLevel: memory.LevelShortTerm,
		Importance: 0.7, AccessCount: 4, Content: "should upgrade",
	})
	repo.Create(context.Background(), &memory.Memory{
		OwnerID:   100, MemoryType: memory.TypeProfile, MemoryLevel: memory.LevelLongTerm,
		Importance: 0.1, AccessCount: 1, Content: "should downgrade",
	})

	svc := NewConsolidationService(repo)
	result := svc.RunConsolidationCycle(context.Background(), 100)

	if result.Upgraded != 1 {
		t.Fatalf("expected 1 upgraded, got %d", result.Upgraded)
	}
	if result.Downgraded != 1 {
		t.Fatalf("expected 1 downgraded, got %d", result.Downgraded)
	}
}
