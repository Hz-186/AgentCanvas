package memory_usecase

import (
	"context"
	"encoding/json"
	"strings"

	"agentcanvas/internal/domain/memory"
	agenterrors "agentcanvas/internal/pkg/errors"
)

type Service struct {
	memories memory.Repository
}

func NewService(memories memory.Repository) *Service {
	return &Service{memories: memories}
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
		Title: strings.TrimSpace(req.Title), Content: strings.TrimSpace(req.Content), Importance: importance,
		Source: strings.TrimSpace(req.Source), MetadataJSON: req.MetadataJSON,
	}
	if item.Source == "" {
		item.Source = "manual"
	}
	if err := s.memories.Create(ctx, item); err != nil {
		return nil, err
	}
	return item, nil
}

func (s *Service) List(ctx context.Context, ownerID int64, memoryTypes []string, conversationID *int64, limit, offset int) ([]memory.Memory, error) {
	return s.memories.List(ctx, ownerID, memoryTypes, conversationID, limit, offset)
}

func (s *Service) Get(ctx context.Context, ownerID, id int64) (*memory.Memory, error) {
	return s.memories.FindByID(ctx, ownerID, id)
}

func (s *Service) Update(ctx context.Context, ownerID, id int64, req UpdateMemoryRequest) (*memory.Memory, error) {
	item, err := s.Get(ctx, ownerID, id)
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
	return item, nil
}

func (s *Service) Delete(ctx context.Context, ownerID, id int64) error {
	return s.memories.SoftDelete(ctx, ownerID, id)
}
