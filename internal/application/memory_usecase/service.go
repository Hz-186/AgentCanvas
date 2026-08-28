package memory_usecase

import (
	"context"
	"fmt"
	"strings"
	"time"

	"agentcanvas/internal/domain/memory"
	agenterrors "agentcanvas/internal/pkg/errors"
)

type Service struct {
	memories   memory.Repository
	cache      memory.Cache
	recallLogs memory.RecallLogRepository
}

func NewServiceWithCache(memories memory.Repository, cache memory.Cache) *Service {
	return &Service{memories: memories, cache: cache}
}

type ListMemoryFilter struct {
	MemoryTypes          []string
	SourceConversationID *int64
	SourceProjectID      *int64
	Statuses             []string
	ScopeTypes           []string
	ScopeID              *int64
	Sources              []string
	Limit                int
	Offset               int
}

func (s *Service) ConfigureRecallLogs(logs memory.RecallLogRepository) {
	s.recallLogs = logs
}

func (s *Service) ListRecallLogs(ctx context.Context, ownerID, memoryID int64, limit int) ([]memory.RecallLog, error) {
	if s.recallLogs == nil {
		return []memory.RecallLog{}, nil
	}
	return s.recallLogs.List(ctx, ownerID, memoryID, limit)
}

func (s *Service) SetRecallFeedback(ctx context.Context, ownerID, id int64, feedback string) error {
	feedback = strings.TrimSpace(feedback)
	if s.recallLogs == nil || (feedback != "helpful" && feedback != "irrelevant" && feedback != "incorrect" && feedback != "") {
		return agenterrors.ErrInvalidInput
	}
	return s.recallLogs.SetFeedback(ctx, ownerID, id, feedback)
}

func (s *Service) ListFiltered(ctx context.Context, ownerID int64, filter ListMemoryFilter) ([]memory.Memory, error) {
	if repository, ok := s.memories.(memory.FilteredRepository); ok {
		return repository.ListFiltered(ctx, ownerID, memory.ListFilter{
			MemoryTypes: filter.MemoryTypes, SourceConversationID: filter.SourceConversationID, SourceProjectID: filter.SourceProjectID, Statuses: filter.Statuses,
			ScopeTypes: filter.ScopeTypes, ScopeID: filter.ScopeID, Sources: filter.Sources, Limit: filter.Limit, Offset: filter.Offset,
		})
	}
	items, err := s.memories.List(ctx, ownerID, filter.MemoryTypes, filter.SourceConversationID, 100, 0)
	if err != nil {
		return nil, err
	}
	filtered := make([]memory.Memory, 0, len(items))
	for i := range items {
		item := items[i]
		if !containsMemoryFilter(filter.Statuses, normalizedMemoryStatus(item.Status)) || !containsMemoryFilter(filter.ScopeTypes, normalizedMemoryScope(item.ScopeType)) || !containsMemoryFilter(filter.Sources, item.Source) {
			continue
		}
		if filter.ScopeID != nil && item.ScopeID != *filter.ScopeID {
			continue
		}
		if filter.SourceProjectID != nil && (item.SourceProjectID == nil || *item.SourceProjectID != *filter.SourceProjectID) {
			continue
		}
		filtered = append(filtered, item)
	}
	limit, offset := filter.Limit, filter.Offset
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	if offset >= len(filtered) {
		return []memory.Memory{}, nil
	}
	end := min(len(filtered), offset+limit)
	return filtered[offset:end], nil
}

func (s *Service) Get(ctx context.Context, ownerID, id int64) (*memory.Memory, error) {
	if s.cache != nil {
		itemKey := fmt.Sprintf("id:%d", id)
		if items, hit, err := s.cache.Get(ctx, ownerID, itemKey); err == nil && hit && len(items) > 0 {
			return &items[0], nil
		}
	}
	item, err := s.memories.FindByID(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	if s.cache != nil {
		_ = s.cache.Set(ctx, ownerID, fmt.Sprintf("id:%d", id), []memory.Memory{*item}, 5*time.Minute)
	}
	return item, nil
}

func normalizedMemoryStatus(value string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return memory.StatusActive
}

func normalizedMemoryScope(value string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return memory.ScopeUser
}

func containsMemoryFilter(values []string, value string) bool {
	if len(values) == 0 {
		return true
	}
	for _, candidate := range values {
		if strings.TrimSpace(candidate) == value {
			return true
		}
	}
	return false
}
