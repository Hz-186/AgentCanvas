package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type ArchivalIndex interface {
	Index(ctx context.Context, item Memory) error
	Search(ctx context.Context, ownerID int64, query string, limit int) ([]int64, error)
	Delete(ctx context.Context, memoryID int64) error
}

type RuntimeService struct {
	Memories  Repository
	Logs      WriteLogRepository
	Retriever SemanticRetriever
	Archival  ArchivalIndex
}

type ReadRequest struct {
	OwnerID        int64
	ConversationID *int64
	MemoryTypes    []string
	Query          string
	Limit          int
}

type ReadResult struct {
	Memories      []Memory `json:"memories"`
	MemoryContext string   `json:"memory_context"`
	Count         int      `json:"count"`
	Query         string   `json:"query,omitempty"`
}

type WriteRequest struct {
	OwnerID        int64
	ConversationID *int64
	RunID          int64
	MemoryID       int64
	MemoryType     string
	Title          string
	Content        string
	Importance     float64
	Reason         string
	Source         string
	SourceKey      *string
}

type WriteResult struct {
	Memory Memory `json:"memory"`
	Action string `json:"action"`
}

func (s RuntimeService) Read(ctx context.Context, req ReadRequest) (ReadResult, error) {
	if s.Memories == nil {
		return ReadResult{}, fmt.Errorf("memory repository is not configured")
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}
	query := strings.TrimSpace(req.Query)
	var ids []int64
	if query != "" {
		var err error
		if onlyArchival(req.MemoryTypes) && s.Archival != nil {
			ids, err = s.Archival.Search(ctx, req.OwnerID, query, limit)
		}
		if (err != nil || len(ids) == 0) && s.Retriever != nil {
			ids, err = s.Retriever.Search(ctx, req.OwnerID, query, req.MemoryTypes, limit)
		}
		if err == nil && len(ids) > 0 {
			items := s.fetchValid(ctx, req, ids, limit)
			return s.readResult(ctx, req.OwnerID, items, query), nil
		}
	}
	items, err := s.Memories.ListForRead(ctx, req.OwnerID, req.MemoryTypes, req.ConversationID, limit)
	if err != nil {
		return ReadResult{}, err
	}
	return s.readResult(ctx, req.OwnerID, items, query), nil
}

func (s RuntimeService) Write(ctx context.Context, req WriteRequest) (WriteResult, error) {
	if s.Memories == nil {
		return WriteResult{}, fmt.Errorf("memory repository is not configured")
	}
	memoryType := strings.TrimSpace(req.MemoryType)
	content := strings.TrimSpace(req.Content)
	if memoryType == "" || content == "" {
		return WriteResult{}, fmt.Errorf("memory_type and content are required")
	}
	action := WriteActionCreate
	var beforeJSON json.RawMessage
	item := &Memory{OwnerID: req.OwnerID, ConversationID: req.ConversationID}
	if req.MemoryID > 0 {
		existing, err := s.Memories.FindByID(ctx, req.OwnerID, req.MemoryID)
		if err != nil {
			return WriteResult{}, err
		}
		beforeJSON, _ = json.Marshal(existing)
		item = existing
		action = WriteActionUpdate
	}
	item.MemoryType = memoryType
	item.MemoryLevel = LevelLongTerm
	item.Title = strings.TrimSpace(req.Title)
	item.Content = content
	item.Importance = req.Importance
	if item.Importance <= 0 {
		item.Importance = 0.5
	}
	if item.Importance > 1 {
		item.Importance = 1
	}
	item.Source = strings.TrimSpace(req.Source)
	item.SourceKey = req.SourceKey
	if item.Source == "" {
		item.Source = "agent_tool"
	}
	var err error
	if action == WriteActionCreate {
		err = s.Memories.Create(ctx, item)
	} else {
		err = s.Memories.Update(ctx, item)
	}
	if err != nil {
		return WriteResult{}, err
	}
	if s.Retriever != nil {
		if err := s.Retriever.Index(ctx, *item); err != nil {
			return WriteResult{}, err
		}
	}
	if s.Archival != nil {
		if item.MemoryType == TypeArchival {
			if err := s.Archival.Index(ctx, *item); err != nil {
				return WriteResult{}, err
			}
		} else if action == WriteActionUpdate {
			_ = s.Archival.Delete(ctx, item.ID)
		}
	}
	afterJSON, _ := json.Marshal(item)
	if s.Logs != nil {
		_ = s.Logs.Create(ctx, &WriteLog{OwnerID: req.OwnerID, MemoryID: item.ID, RunID: req.RunID, Action: action, BeforeJSON: beforeJSON, AfterJSON: afterJSON, Reason: strings.TrimSpace(req.Reason)})
	}
	return WriteResult{Memory: *item, Action: action}, nil
}

func (s RuntimeService) Delete(ctx context.Context, ownerID, memoryID int64) error {
	if s.Memories == nil {
		return fmt.Errorf("memory repository is not configured")
	}
	if err := s.Memories.SoftDelete(ctx, ownerID, memoryID); err != nil {
		return err
	}
	if s.Retriever != nil {
		if err := s.Retriever.Delete(ctx, memoryID); err != nil {
			return err
		}
	}
	if s.Archival != nil {
		if err := s.Archival.Delete(ctx, memoryID); err != nil {
			return err
		}
	}
	return nil
}

func (s RuntimeService) fetchValid(ctx context.Context, req ReadRequest, ids []int64, limit int) []Memory {
	items := make([]Memory, 0, len(ids))
	now := time.Now()
	for _, id := range ids {
		item, err := s.Memories.FindByID(ctx, req.OwnerID, id)
		if err != nil || item.DeletedAt != nil || (item.ExpiresAt != nil && !item.ExpiresAt.After(now)) || !matchesType(item.MemoryType, req.MemoryTypes) || !matchesConversation(item.ConversationID, req.ConversationID) {
			continue
		}
		items = append(items, *item)
		if len(items) == limit {
			break
		}
	}
	return items
}

func (s RuntimeService) readResult(ctx context.Context, ownerID int64, items []Memory, query string) ReadResult {
	ids := make([]int64, 0, len(items))
	lines := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
		lines = append(lines, item.Content)
	}
	_ = s.Memories.MarkUsed(ctx, ownerID, ids)
	return ReadResult{Memories: items, MemoryContext: strings.Join(lines, "\n"), Count: len(items), Query: query}
}

func onlyArchival(types []string) bool {
	return len(types) == 1 && strings.TrimSpace(types[0]) == TypeArchival
}

func matchesType(value string, types []string) bool {
	if len(types) == 0 {
		return true
	}
	for _, memoryType := range types {
		if value == strings.TrimSpace(memoryType) {
			return true
		}
	}
	return false
}

func matchesConversation(item, requested *int64) bool {
	if requested == nil || item == nil {
		return true
	}
	return *item == *requested
}
