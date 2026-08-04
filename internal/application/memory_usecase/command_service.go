package memory_usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agentcanvas/internal/domain/memory"
)

// MemoryCommandService is the single application entry point for effective
// memory mutations. The repository owns the MySQL transaction and Context
// Index Outbox registration, so callers never perform a second index write.
type MemoryCommandService struct {
	runtime memory.RuntimeService
}

func writeLifecycleLog(ctx context.Context, logs memory.WriteLogRepository, ownerID, memoryID int64, action string, before, after memory.Memory, reason string) error {
	beforeJSON, err := json.Marshal(before)
	if err != nil {
		return err
	}
	afterJSON, err := json.Marshal(after)
	if err != nil {
		return err
	}
	return logs.Create(ctx, &memory.WriteLog{OwnerID: ownerID, MemoryID: memoryID, Action: action, BeforeJSON: beforeJSON, AfterJSON: afterJSON, Reason: reason})
}

func NewMemoryCommandService(memories memory.Repository, logs memory.WriteLogRepository) *MemoryCommandService {
	return &MemoryCommandService{runtime: memory.RuntimeService{Memories: memories, Logs: logs}}
}

func (s *MemoryCommandService) Execute(ctx context.Context, request memory.WriteRequest) (memory.WriteResult, error) {
	if s == nil {
		return memory.WriteResult{}, fmt.Errorf("memory command service is not configured")
	}
	if request.MemoryID == 0 && request.MemoryType == memory.TypeSummary {
		return memory.WriteResult{}, fmt.Errorf("summary_memory is read-only compatibility data; use conversation compaction")
	}
	request.ScopeType = strings.TrimSpace(request.ScopeType)
	if request.ScopeType == "" {
		if request.ConversationID != nil && *request.ConversationID > 0 {
			request.ScopeType, request.ScopeID = memory.ScopeConversation, *request.ConversationID
		} else {
			request.ScopeType, request.ScopeID = memory.ScopeUser, request.OwnerID
		}
	}
	if !validMemoryScope(request.ScopeType, request.ScopeID, request.OwnerID) {
		return memory.WriteResult{}, fmt.Errorf("invalid memory scope")
	}
	if request.Status == "" {
		request.Status = memory.StatusActive
	}
	return s.runtime.Write(ctx, request)
}

func validMemoryScope(scopeType string, scopeID, ownerID int64) bool {
	switch scopeType {
	case memory.ScopeUser:
		return scopeID == 0 || scopeID == ownerID
	case memory.ScopeAgent, memory.ScopeConversation:
		return scopeID > 0
	default:
		return false
	}
}

func (s *MemoryCommandService) Revoke(ctx context.Context, ownerID, memoryID int64, reason string) error {
	if s == nil || s.runtime.Memories == nil {
		return fmt.Errorf("memory command service is not configured")
	}
	item, err := s.runtime.Memories.FindByID(ctx, ownerID, memoryID)
	if err != nil {
		return err
	}
	before := *item
	item.Status = memory.StatusRevoked
	item.UpdatedAt = time.Now().UTC()
	if err := s.runtime.Memories.Update(ctx, item); err != nil {
		return err
	}
	if s.runtime.Logs != nil {
		return writeLifecycleLog(ctx, s.runtime.Logs, ownerID, item.ID, "revoke", before, *item, reason)
	}
	return nil
}

func (s *MemoryCommandService) Supersede(ctx context.Context, ownerID, memoryID, replacementID int64, reason string) error {
	if s == nil || s.runtime.Memories == nil || replacementID <= 0 {
		return fmt.Errorf("memory command service is not configured")
	}
	item, err := s.runtime.Memories.FindByID(ctx, ownerID, memoryID)
	if err != nil {
		return err
	}
	before := *item
	item.Status = memory.StatusSuperseded
	item.UpdatedAt = time.Now().UTC()
	if err := s.runtime.Memories.Update(ctx, item); err != nil {
		return err
	}
	if s.runtime.Logs != nil {
		return writeLifecycleLog(ctx, s.runtime.Logs, ownerID, item.ID, "supersede", before, *item, fmt.Sprintf("%s; replacement_id=%d", reason, replacementID))
	}
	return nil
}
