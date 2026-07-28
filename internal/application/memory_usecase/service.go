package memory_usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agentcanvas/internal/domain/memory"
	agenterrors "agentcanvas/internal/pkg/errors"
)

type Service struct {
	memories   memory.Repository
	cache      memory.Cache
	retriever  memory.SemanticRetriever
	commands   *MemoryCommandService
	recallLogs memory.RecallLogRepository
}

func NewService(memories memory.Repository) *Service {
	return &Service{memories: memories, commands: NewMemoryCommandService(memories, nil)}
}

func NewServiceWithCache(memories memory.Repository, cache memory.Cache) *Service {
	return &Service{memories: memories, cache: cache, commands: NewMemoryCommandService(memories, nil)}
}

func NewServiceWithCacheAndRetriever(memories memory.Repository, cache memory.Cache, retriever memory.SemanticRetriever) *Service {
	return &Service{memories: memories, cache: cache, retriever: retriever, commands: NewMemoryCommandService(memories, nil)}
}

type CreateMemoryRequest struct {
	ConversationID *int64          `json:"conversation_id"`
	ScopeType      string          `json:"scope_type"`
	ScopeID        int64           `json:"scope_id"`
	MemoryType     string          `json:"memory_type" binding:"required"`
	Title          string          `json:"title"`
	Content        string          `json:"content" binding:"required"`
	Importance     float64         `json:"importance"`
	Source         string          `json:"source"`
	MetadataJSON   json.RawMessage `json:"metadata_json"`
}

type UpdateMemoryRequest struct {
	ConversationID *int64          `json:"conversation_id"`
	ScopeType      string          `json:"scope_type"`
	ScopeID        *int64          `json:"scope_id"`
	MemoryType     string          `json:"memory_type"`
	Title          string          `json:"title"`
	Content        string          `json:"content"`
	Importance     *float64        `json:"importance"`
	Source         string          `json:"source"`
	MetadataJSON   json.RawMessage `json:"metadata_json"`
}

type ListMemoryFilter struct {
	MemoryTypes    []string
	ConversationID *int64
	Statuses       []string
	ScopeTypes     []string
	ScopeID        *int64
	Sources        []string
	Limit          int
	Offset         int
}

func (s *Service) ConfigureCommands(commands *MemoryCommandService) {
	if commands != nil {
		s.commands = commands
	}
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

func (s *Service) Create(ctx context.Context, ownerID int64, req CreateMemoryRequest) (*memory.Memory, error) {
	if ownerID <= 0 || strings.TrimSpace(req.MemoryType) == "" || strings.TrimSpace(req.Content) == "" {
		return nil, agenterrors.ErrInvalidInput
	}
	importance := req.Importance
	if importance == 0 {
		importance = 0.5
	}
	if importance < 0 || importance > 1 {
		return nil, agenterrors.ErrInvalidInput
	}
	result, err := s.commands.Execute(ctx, memory.WriteRequest{OwnerID: ownerID, ConversationID: req.ConversationID,
		MemoryType: strings.TrimSpace(req.MemoryType), Title: strings.TrimSpace(req.Title), Content: strings.TrimSpace(req.Content),
		Importance: importance, Source: manualMemorySource(req.Source), MetadataJSON: req.MetadataJSON, ScopeType: req.ScopeType, ScopeID: req.ScopeID, Reason: "manual create"})
	if err != nil {
		return nil, err
	}
	s.invalidateCache(ctx, ownerID)
	return &result.Memory, nil
}

func (s *Service) List(ctx context.Context, ownerID int64, memoryTypes []string, conversationID *int64, limit, offset int) ([]memory.Memory, error) {
	if s.cache != nil {
		cacheKey := s.listCacheKey(memoryTypes, conversationID, limit, offset)
		if items, hit, err := s.cache.Get(ctx, ownerID, cacheKey); err == nil && hit {
			return items, nil
		}
	}
	items, err := s.memories.List(ctx, ownerID, memoryTypes, conversationID, limit, offset)
	if err != nil {
		return nil, err
	}
	if s.cache != nil {
		_ = s.cache.Set(ctx, ownerID, s.listCacheKey(memoryTypes, conversationID, limit, offset), items, 30*time.Second)
	}
	return items, nil
}

func (s *Service) ListFiltered(ctx context.Context, ownerID int64, filter ListMemoryFilter) ([]memory.Memory, error) {
	if repository, ok := s.memories.(memory.FilteredRepository); ok {
		return repository.ListFiltered(ctx, ownerID, memory.ListFilter{
			MemoryTypes: filter.MemoryTypes, ConversationID: filter.ConversationID, Statuses: filter.Statuses,
			ScopeTypes: filter.ScopeTypes, ScopeID: filter.ScopeID, Sources: filter.Sources, Limit: filter.Limit, Offset: filter.Offset,
		})
	}
	items, err := s.memories.List(ctx, ownerID, filter.MemoryTypes, filter.ConversationID, 100, 0)
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

func (s *Service) GetMany(ctx context.Context, ownerID int64, ids []int64) ([]memory.Memory, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	byID := make(map[int64]memory.Memory, len(ids))
	misses := make([]int64, 0, len(ids))
	seenMiss := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if s.cache != nil {
			if items, hit, err := s.cache.Get(ctx, ownerID, fmt.Sprintf("id:%d", id)); err == nil && hit && len(items) > 0 {
				byID[id] = items[0]
				continue
			}
		}
		if !seenMiss[id] {
			seenMiss[id] = true
			misses = append(misses, id)
		}
	}
	if len(misses) > 0 {
		items, err := s.memories.FindByIDs(ctx, ownerID, misses)
		if err != nil {
			return nil, err
		}
		for _, item := range items {
			byID[item.ID] = item
			if s.cache != nil {
				_ = s.cache.Set(ctx, ownerID, fmt.Sprintf("id:%d", item.ID), []memory.Memory{item}, 5*time.Minute)
			}
		}
	}
	ordered := make([]memory.Memory, 0, len(ids))
	for _, id := range ids {
		if item, ok := byID[id]; ok {
			ordered = append(ordered, item)
		}
	}
	return ordered, nil
}

func (s *Service) Update(ctx context.Context, ownerID, id int64, req UpdateMemoryRequest) (*memory.Memory, error) {
	item, err := s.memories.FindByID(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	if req.ConversationID != nil {
		item.ConversationID = req.ConversationID
	}
	if value := strings.TrimSpace(req.ScopeType); value != "" {
		item.ScopeType = value
	}
	if req.ScopeID != nil {
		item.ScopeID = *req.ScopeID
	}
	if value := strings.TrimSpace(req.MemoryType); value != "" {
		item.MemoryType = value
	}
	if value := strings.TrimSpace(req.Content); value != "" {
		item.Content = value
	}
	item.Title = strings.TrimSpace(req.Title)
	item.Source = strings.TrimSpace(req.Source)
	if req.Importance != nil {
		if *req.Importance < 0 || *req.Importance > 1 {
			return nil, agenterrors.ErrInvalidInput
		}
		item.Importance = *req.Importance
	}
	if len(req.MetadataJSON) > 0 {
		item.MetadataJSON = req.MetadataJSON
	}
	result, err := s.commands.Execute(ctx, memory.WriteRequest{OwnerID: ownerID, ConversationID: item.ConversationID, MemoryID: item.ID,
		MemoryType: item.MemoryType, Title: item.Title, Content: item.Content, Importance: item.Importance, Source: manualMemorySource(item.Source), MetadataJSON: item.MetadataJSON,
		ScopeType: item.ScopeType, ScopeID: item.ScopeID, Status: item.Status, SupersedesID: item.SupersedesID, Reason: "manual update"})
	if err != nil {
		return nil, err
	}
	s.invalidateCache(ctx, ownerID)
	return &result.Memory, nil
}

func (s *Service) Delete(ctx context.Context, ownerID, id int64) error {
	if err := s.commands.Revoke(ctx, ownerID, id, "manual revoke"); err != nil {
		return err
	}
	s.invalidateCache(ctx, ownerID)
	return nil
}

func (s *Service) listCacheKey(memoryTypes []string, conversationID *int64, limit, offset int) string {
	cid := "_"
	if conversationID != nil {
		cid = fmt.Sprintf("%d", *conversationID)
	}
	return fmt.Sprintf("list:%s:%s:%d:%d", strings.Join(memoryTypes, ","), cid, limit, offset)
}

func (s *Service) invalidateCache(ctx context.Context, ownerID int64) {
	if s.cache != nil {
		_ = s.cache.InvalidateOwner(ctx, ownerID)
	}
}

func manualMemorySource(value string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return "manual"
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
