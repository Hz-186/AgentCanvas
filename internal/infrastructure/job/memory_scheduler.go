package job

import (
	"context"
	"log/slog"
	"sync"
	"time"

	memoryusecase "agentcanvas/internal/application/memory_usecase"
	"agentcanvas/internal/domain/memory"
)

type MemoryScheduler struct {
	memories      memory.Repository
	consolidation *memoryusecase.ConsolidationService

	mu       sync.Mutex
	running  bool
	stopCh   chan struct{}
	stopOnce sync.Once
	interval time.Duration
	logger   *slog.Logger
}

type MemorySchedulerConfig struct {
	ConsolidationInterval time.Duration
	Logger                *slog.Logger
}

func NewMemoryScheduler(
	memories memory.Repository,
	cfg MemorySchedulerConfig,
) *MemoryScheduler {
	if cfg.ConsolidationInterval <= 0 {
		cfg.ConsolidationInterval = 1 * time.Hour
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &MemoryScheduler{
		memories:      memories,
		consolidation: memoryusecase.NewConsolidationService(memories),
		interval:      cfg.ConsolidationInterval,
		logger:        logger,
		stopCh:        make(chan struct{}),
	}
}

func (s *MemoryScheduler) Start(ctx context.Context) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.stopCh = make(chan struct{})
	s.stopOnce = sync.Once{}
	stopCh := s.stopCh
	s.mu.Unlock()

	s.logger.Info("memory scheduler started",
		"interval", s.interval.String())

	go func() {
		defer func() {
			s.mu.Lock()
			s.running = false
			s.mu.Unlock()
		}()

		for {
			select {
			case <-stopCh:
				s.logger.Info("memory scheduler stopped")
				return
			case <-ctx.Done():
				s.logger.Info("memory scheduler context cancelled")
				return
			case <-time.After(s.interval):
				s.runConsolidation(ctx)
				s.runExpirationCleanup(ctx)
			}
		}
	}()
}

func (s *MemoryScheduler) Stop() {
	s.mu.Lock()
	running := s.running
	stopCh := s.stopCh
	s.mu.Unlock()
	if running && stopCh != nil {
		s.stopOnce.Do(func() { close(stopCh) })
	}
}

func (s *MemoryScheduler) IsRunning() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
}

func (s *MemoryScheduler) runConsolidation(ctx context.Context) {
	ownerIDs, err := s.getActiveOwners(ctx)
	if err != nil {
		s.logger.Error("memory scheduler: failed to get active owners", "error", err)
		return
	}
	if len(ownerIDs) == 0 {
		return
	}

	totalResult := memoryusecase.ConsolidationResult{}
	for _, ownerID := range ownerIDs {
		result := s.consolidation.RunConsolidationCycle(ctx, ownerID)
		totalResult.Upgraded += result.Upgraded
		totalResult.Decayed += result.Decayed
		totalResult.Downgraded += result.Downgraded
		totalResult.Expired += result.Expired
	}

	if totalResult.Upgraded > 0 || totalResult.Decayed > 0 || totalResult.Downgraded > 0 || totalResult.Expired > 0 {
		s.logger.Info("memory consolidation cycle completed",
			"upgraded", totalResult.Upgraded,
			"decayed", totalResult.Decayed,
			"downgraded", totalResult.Downgraded,
			"expired", totalResult.Expired)
	}
}

func (s *MemoryScheduler) runExpirationCleanup(ctx context.Context) {
	ownerIDs, err := s.getActiveOwners(ctx)
	if err != nil {
		return
	}
	var totalCleaned int64
	for _, ownerID := range ownerIDs {
		cleaned, err := s.memories.MarkExpired(ctx, ownerID, 7)
		if err == nil {
			totalCleaned += cleaned
		}
	}
	if totalCleaned > 0 {
		s.logger.Info("memory expiration cleanup completed",
			"cleaned", totalCleaned)
	}
}

func (s *MemoryScheduler) getActiveOwners(ctx context.Context) ([]int64, error) {
	return s.memories.ListActiveOwnerIDs(ctx, 1000)
}
