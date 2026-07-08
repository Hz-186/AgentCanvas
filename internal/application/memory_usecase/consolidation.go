package memory_usecase

import (
	"context"

	"agentcanvas/internal/domain/memory"
)

type ConsolidationService struct {
	memories memory.Repository
}

func NewConsolidationService(memories memory.Repository) *ConsolidationService {
	return &ConsolidationService{memories: memories}
}

func (s *ConsolidationService) UpgradeShortTermToLongTerm(ctx context.Context, ownerID int64, minAccessCount int, minImportance float64) (int64, error) {
	items, err := s.memories.ListByLevel(ctx, ownerID, memory.LevelShortTerm, nil, 100)
	if err != nil {
		return 0, err
	}
	var upgraded int64
	for _, item := range items {
		if item.AccessCount >= minAccessCount && item.Importance >= minImportance {
			item.MemoryLevel = memory.LevelLongTerm
			if err := s.memories.Update(ctx, &item); err == nil {
				_ = s.memories.IncrementConsolidationCount(ctx, ownerID, item.ID)
				upgraded++
			}
		}
	}
	return upgraded, nil
}

func (s *ConsolidationService) ExpireShortTermMemories(ctx context.Context, ownerID int64, maxAgeDays int) (int64, error) {
	return s.memories.MarkExpired(ctx, ownerID, maxAgeDays)
}

func (s *ConsolidationService) DecayLongTermImportance(ctx context.Context, ownerID int64, decayRate float64) (int64, error) {
	return s.memories.UpdateDecayedImportance(ctx, ownerID, decayRate)
}

func (s *ConsolidationService) DowngradeWeakLongTerm(ctx context.Context, ownerID int64, minImportance float64) (int64, error) {
	items, err := s.memories.ListByLevel(ctx, ownerID, memory.LevelLongTerm, nil, 200)
	if err != nil {
		return 0, err
	}
	var downgraded int64
	for _, item := range items {
		if item.Importance < minImportance && item.AccessCount < 3 {
			item.MemoryLevel = memory.LevelShortTerm
			if err := s.memories.Update(ctx, &item); err == nil {
				downgraded++
			}
		}
	}
	return downgraded, nil
}

func (s *ConsolidationService) RunConsolidationCycle(ctx context.Context, ownerID int64) ConsolidationResult {
	result := ConsolidationResult{}

	upgraded, err := s.UpgradeShortTermToLongTerm(ctx, ownerID, 2, 0.6)
	if err == nil {
		result.Upgraded = int(upgraded)
	}

	decayed, err := s.DecayLongTermImportance(ctx, ownerID, 0.01)
	if err == nil {
		result.Decayed = int(decayed)
	}

	downgraded, err := s.DowngradeWeakLongTerm(ctx, ownerID, 0.15)
	if err == nil {
		result.Downgraded = int(downgraded)
	}

	expired, err := s.ExpireShortTermMemories(ctx, ownerID, 7)
	if err == nil {
		result.Expired = int(expired)
	}

	return result
}

type ConsolidationResult struct {
	Upgraded   int `json:"upgraded"`
	Decayed    int `json:"decayed"`
	Downgraded int `json:"downgraded"`
	Expired    int `json:"expired"`
}
