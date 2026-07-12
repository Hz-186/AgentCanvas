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
	memories  memory.Repository
	cache     memory.Cache
	retriever memory.SemanticRetriever
}

func NewService(memories memory.Repository) *Service {
	return &Service{memories: memories}
}

func NewServiceWithCache(memories memory.Repository, cache memory.Cache) *Service {
	return &Service{memories: memories, cache: cache}
}

func NewServiceWithCacheAndRetriever(memories memory.Repository, cache memory.Cache, retriever memory.SemanticRetriever) *Service {
	return &Service{memories: memories, cache: cache, retriever: retriever}
}

type CreateMemoryRequest struct {
	ConversationID *int64          `json:"conversation_id"`
	MemoryType     string          `json:"memory_type" binding:"required"`
	Title          string          `json:"title"`
	Content        string          `json:"content" binding:"required"`
	Importance     float64         `json:"importance"`
	Source         string          `json:"source"`
	MetadataJSON   json.RawMessage `json:"metadata_json"`
}

type UpdateMemoryRequest struct {
	ConversationID *int64          `json:"conversation_id"`
	MemoryType     string          `json:"memory_type"`
	Title          string          `json:"title"`
	Content        string          `json:"content"`
	Importance     *float64        `json:"importance"`
	Source         string          `json:"source"`
	MetadataJSON   json.RawMessage `json:"metadata_json"`
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
	item := &memory.Memory{
		OwnerID: ownerID, ConversationID: req.ConversationID, MemoryType: strings.TrimSpace(req.MemoryType),
		MemoryLevel: memory.LevelLongTerm,
		Title:       strings.TrimSpace(req.Title), Content: strings.TrimSpace(req.Content), Importance: importance,
		Source: strings.TrimSpace(req.Source), MetadataJSON: req.MetadataJSON,
	}
	if item.Source == "" {
		item.Source = "manual"
	}
	if err := s.memories.Create(ctx, item); err != nil {
		return nil, err
	}
	if err := s.indexMemory(ctx, item); err != nil {
		return nil, err
	}
	s.invalidateCache(ctx, ownerID)
	return item, nil
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

func (s *Service) Update(ctx context.Context, ownerID, id int64, req UpdateMemoryRequest) (*memory.Memory, error) {
	item, err := s.memories.FindByID(ctx, ownerID, id)
	if err != nil {
		return nil, err
	}
	if req.ConversationID != nil {
		item.ConversationID = req.ConversationID
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
	if err := s.memories.Update(ctx, item); err != nil {
		return nil, err
	}
	if err := s.indexMemory(ctx, item); err != nil {
		return nil, err
	}
	s.invalidateCache(ctx, ownerID)
	return item, nil
}

func (s *Service) Delete(ctx context.Context, ownerID, id int64) error {
	if err := s.memories.SoftDelete(ctx, ownerID, id); err != nil {
		return err
	}
	if s.retriever != nil {
		if err := s.retriever.Delete(ctx, id); err != nil {
			return err
		}
	}
	s.invalidateCache(ctx, ownerID)
	return nil
}

func (s *Service) indexMemory(ctx context.Context, item *memory.Memory) error {
	if s.retriever == nil || item == nil {
		return nil
	}
	if item.MemoryLevel == "" {
		item.MemoryLevel = memory.LevelLongTerm
	}
	return s.retriever.Index(ctx, *item)
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
