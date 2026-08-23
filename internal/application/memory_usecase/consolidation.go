package memory_usecase

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/observability"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type ConsolidationService struct {
	memories memory.Repository
}

func NewConsolidationService(memories memory.Repository) *ConsolidationService {
	return &ConsolidationService{memories: memories}
}

func (s *ConsolidationService) UpgradeShortTermToLongTerm(ctx context.Context, ownerID int64, minRecallCount int, minImportance float64) (int64, error) {
	items, err := s.memories.ListByLevel(ctx, ownerID, memory.TierShortTerm, nil, 100)
	if err != nil {
		return 0, err
	}
	var upgraded int64
	for _, item := range items {
		if item.RecallCount >= minRecallCount && item.Importance >= minImportance {
			item.RetentionTier = memory.TierLongTerm
			if err := s.memories.Update(ctx, &item); err != nil {
				return upgraded, err
			}
			if err := s.memories.IncrementPromotionCount(ctx, ownerID, item.ID); err != nil {
				return upgraded, err
			}
			upgraded++
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
	items, err := s.memories.ListByLevel(ctx, ownerID, memory.TierLongTerm, nil, 200)
	if err != nil {
		return 0, err
	}
	var downgraded int64
	for _, item := range items {
		if item.Importance < minImportance && item.RecallCount < 3 {
			item.RetentionTier = memory.TierShortTerm
			if err := s.memories.Update(ctx, &item); err != nil {
				return downgraded, err
			}
			downgraded++
		}
	}
	return downgraded, nil
}

func (s *ConsolidationService) RunConsolidationCycle(ctx context.Context, ownerID int64) (ConsolidationResult, error) {
	result := ConsolidationResult{}
	var joined error

	upgraded, err := s.UpgradeShortTermToLongTerm(ctx, ownerID, 2, 0.6)
	if err == nil {
		result.Upgraded = int(upgraded)
	} else {
		joined = errors.Join(joined, err)
	}

	decayed, err := s.DecayLongTermImportance(ctx, ownerID, 0.01)
	if err == nil {
		result.Decayed = int(decayed)
	} else {
		joined = errors.Join(joined, err)
	}

	downgraded, err := s.DowngradeWeakLongTerm(ctx, ownerID, 0.15)
	if err == nil {
		result.Downgraded = int(downgraded)
	} else {
		joined = errors.Join(joined, err)
	}

	expired, err := s.ExpireShortTermMemories(ctx, ownerID, 7)
	if err == nil {
		result.Expired = int(expired)
	} else {
		joined = errors.Join(joined, err)
	}

	return result, joined
}

type ConsolidationResult struct {
	Upgraded   int `json:"upgraded"`
	Decayed    int `json:"decayed"`
	Downgraded int `json:"downgraded"`
	Expired    int `json:"expired"`
}

type Scheduler struct {
	service  *ConsolidationService
	redis    *redis.Client
	interval time.Duration
	logger   *slog.Logger
	lockKey  string
}

func NewScheduler(memories memory.Repository, redisClient *redis.Client, interval time.Duration, logger *slog.Logger) *Scheduler {
	if interval <= 0 {
		interval = time.Hour
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Scheduler{service: NewConsolidationService(memories), redis: redisClient, interval: interval, logger: logger, lockKey: "memory:scheduler:consolidation"}
}

func (s *Scheduler) Run(ctx context.Context) {
	if s == nil || s.service == nil {
		return
	}
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runOnce(ctx)
		}
	}
}

func (s *Scheduler) runOnce(ctx context.Context) {
	observability.MemoryRuntimeMetrics.RecordSchedulerRun()
	lockToken := ""
	if s.redis != nil {
		lockToken = uuid.NewString()
		locked, err := s.redis.SetNX(ctx, s.lockKey, lockToken, s.interval).Result()
		if err != nil || !locked {
			if err != nil {
				observability.MemoryRuntimeMetrics.RecordSchedulerLockFailure()
				s.logger.Error("memory scheduler lock failed", "error", err)
			}
			return
		}
		defer func() {
			const releaseLock = `if redis.call("get", KEYS[1]) == ARGV[1] then return redis.call("del", KEYS[1]) else return 0 end`
			if err := s.redis.Eval(context.Background(), releaseLock, []string{s.lockKey}, lockToken).Err(); err != nil {
				observability.MemoryRuntimeMetrics.RecordSchedulerLockFailure()
				s.logger.Error("memory scheduler lock release failed", "error", err)
			}
		}()
	}
	owners, err := s.service.memories.ListActiveOwnerIDs(ctx, 1000)
	if err != nil {
		observability.MemoryRuntimeMetrics.RecordSchedulerFailure()
		s.logger.Error("list memory owners failed", "error", err)
		return
	}
	for _, ownerID := range owners {
		result, err := s.service.RunConsolidationCycle(ctx, ownerID)
		if err != nil {
			observability.MemoryRuntimeMetrics.RecordSchedulerFailure()
			s.logger.Error("memory consolidation cycle failed", "owner_id", ownerID, "error", err)
			continue
		}
		s.logger.Info("memory consolidation cycle completed", "owner_id", ownerID, "upgraded", result.Upgraded, "decayed", result.Decayed, "downgraded", result.Downgraded, "expired", result.Expired)
	}
}
