package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
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
	// SemanticOnly prevents the legacy "latest N" fallback. Agent runtime
	// reads set this flag so only query-relevant memories enter context.
	SemanticOnly bool
	// AllowLegacyListFallback is reserved for maintenance/import callers. It
	// is intentionally false for all Agent-facing reads.
	AllowLegacyListFallback bool
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
	// ConflictResolution is injected only after a user decision. Supported
	// values are keep_existing:<id>, replace:<id>, and keep_both.
	ConflictResolution string
}

type WriteResult struct {
	Memory   Memory          `json:"memory"`
	Action   string          `json:"action"`
	Conflict *MemoryConflict `json:"conflict,omitempty"`
}

type MemoryConflict struct {
	Existing Memory           `json:"existing"`
	Incoming Memory           `json:"incoming"`
	Options  []ConflictOption `json:"options"`
}

type ConflictOption struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description"`
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
	if query == "" && !req.AllowLegacyListFallback {
		return ReadResult{}, fmt.Errorf("semantic memory query is required")
	}
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
		if req.SemanticOnly || !req.AllowLegacyListFallback {
			if err != nil {
				return ReadResult{}, fmt.Errorf("semantic memory retrieval failed: %w", err)
			}
			return s.readResult(ctx, req.OwnerID, nil, query), nil
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
	resolution := strings.TrimSpace(req.ConflictResolution)
	resolutionTarget := int64(0)
	if strings.HasPrefix(resolution, "keep_existing:") {
		resolutionTarget, _ = strconv.ParseInt(strings.TrimPrefix(resolution, "keep_existing:"), 10, 64)
		if resolutionTarget <= 0 {
			return WriteResult{}, fmt.Errorf("invalid keep_existing memory conflict resolution")
		}
	}
	if strings.HasPrefix(resolution, "replace:") {
		resolutionTarget, _ = strconv.ParseInt(strings.TrimPrefix(resolution, "replace:"), 10, 64)
		if resolutionTarget <= 0 {
			return WriteResult{}, fmt.Errorf("invalid replace memory conflict resolution")
		}
	}
	var conflictParent *int64
	if req.MemoryID == 0 && resolutionTarget > 0 {
		conflict, _, err := s.findConflict(ctx, req, memoryType, content)
		if err != nil {
			return WriteResult{}, err
		}
		if conflict == nil || conflict.Existing.ID != resolutionTarget {
			return WriteResult{}, fmt.Errorf("memory conflict resolution target does not match the detected conflict")
		}
		if strings.HasPrefix(resolution, "keep_existing:") {
			return WriteResult{Memory: conflict.Existing, Action: WriteActionNoop}, nil
		}
		req.MemoryID = resolutionTarget
	}
	if req.MemoryID == 0 {
		conflict, identical, err := s.findConflict(ctx, req, memoryType, content)
		if err != nil {
			return WriteResult{}, err
		}
		if identical != nil {
			if resolution == "keep_both" {
				return WriteResult{Memory: *identical, Action: WriteActionNoop}, nil
			}
			return WriteResult{Memory: *identical, Action: WriteActionNoop}, nil
		}
		if conflict != nil {
			conflictParent = &conflict.Existing.ID
			if resolution == "keep_both" {
				// Preserve the relationship when the user explicitly keeps both.
				// The new row remains independently retrievable, while its lineage
				// allows later review and conflict-aware filtering.
				conflictParent = &conflict.Existing.ID
			} else {
				return WriteResult{Memory: conflict.Incoming, Action: WriteActionConflict, Conflict: conflict}, nil
			}
		}
	}
	action := WriteActionCreate
	var beforeJSON json.RawMessage
	item := &Memory{OwnerID: req.OwnerID, ConversationID: req.ConversationID}
	item.ParentID = conflictParent
	// keep_both is explicitly resolved, so the new version remains readable;
	// ParentID preserves the relationship for audit and later review.
	item.ConflictFlag = false
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

func (s RuntimeService) findConflict(ctx context.Context, req WriteRequest, memoryType, content string) (*MemoryConflict, *Memory, error) {
	if s.Retriever == nil {
		return nil, nil, nil
	}
	ids, err := s.Retriever.Search(ctx, req.OwnerID, content, []string{memoryType}, 5)
	if err != nil || len(ids) == 0 {
		// A failed semantic lookup must not degrade into a broad database scan.
		return nil, nil, err
	}
	items := s.fetchValid(ctx, ReadRequest{OwnerID: req.OwnerID, ConversationID: req.ConversationID, MemoryTypes: []string{memoryType}}, ids, 5)
	incoming := Memory{OwnerID: req.OwnerID, ConversationID: req.ConversationID, MemoryType: memoryType, MemoryLevel: LevelLongTerm,
		Title: strings.TrimSpace(req.Title), Content: content, Importance: req.Importance, Source: strings.TrimSpace(req.Source)}
	for i := range items {
		existing := items[i]
		if normalizeMemoryText(existing.Content) == normalizeMemoryText(content) {
			return nil, &existing, nil
		}
		if !sameMemorySubject(existing, incoming) {
			continue
		}
		existing.ConflictFlag = true
		incoming.ParentID = &existing.ID
		incoming.ConflictFlag = true
		return &MemoryConflict{Existing: existing, Incoming: incoming, Options: []ConflictOption{
			{ID: "keep_existing:" + strconv.FormatInt(existing.ID, 10), Label: "保留原记忆", Description: "忽略这次写入，继续使用现有记忆。"},
			{ID: "replace:" + strconv.FormatInt(existing.ID, 10), Label: "使用新记忆", Description: "用本次内容替换现有记忆。"},
			{ID: "keep_both", Label: "两者都保留", Description: "保存为并列记忆，后续召回时继续区分来源。"},
		}}, nil, nil
	}
	return nil, nil, nil
}

func normalizeMemoryText(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(value), " "))
}

func sameMemorySubject(left, right Memory) bool {
	if left.MemoryType != right.MemoryType {
		return false
	}
	leftTitle, rightTitle := normalizeMemoryText(left.Title), normalizeMemoryText(right.Title)
	if leftTitle != "" && rightTitle != "" && leftTitle == rightTitle {
		return true
	}
	leftWords, rightWords := memoryWords(left.Content), memoryWords(right.Content)
	if len(leftWords) == 0 || len(rightWords) == 0 {
		return false
	}
	common := 0
	for word := range leftWords {
		if rightWords[word] {
			common++
		}
	}
	union := len(leftWords) + len(rightWords) - common
	return common >= 2 && union > 0 && float64(common)/float64(union) >= .30
}

func memoryWords(value string) map[string]bool {
	words := map[string]bool{}
	for _, raw := range strings.Fields(strings.NewReplacer(",", " ", ".", " ", ":", " ", ";", " ", "，", " ", "。", " ").Replace(strings.ToLower(value))) {
		word := strings.Trim(raw, "!?()[]{}\"'")
		if len([]rune(word)) < 2 {
			continue
		}
		words[word] = true
	}
	return words
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
		if err != nil || item.DeletedAt != nil || (item.ExpiresAt != nil && !item.ExpiresAt.After(now)) || item.ConflictFlag || !matchesLevel(item.MemoryLevel) || !matchesType(item.MemoryType, req.MemoryTypes) || !matchesConversation(item.ConversationID, req.ConversationID) {
			continue
		}
		items = append(items, *item)
		if len(items) == limit {
			break
		}
	}
	return items
}

// matchesLevel keeps working memory out of semantic memory recall. Working
// memory is injected through its dedicated runtime channel and must not be
// duplicated or allowed to override the current transcript.
func matchesLevel(level string) bool {
	return level == "" || level == LevelShortTerm || level == LevelLongTerm
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
