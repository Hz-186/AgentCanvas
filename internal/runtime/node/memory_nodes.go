package node

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/runtime/engine"
	runtimeevent "agentcanvas/internal/runtime/event"

	agenterrors "agentcanvas/internal/pkg/errors"
)

type MemoryReadNode struct {
	Memories  memory.Repository
	Retriever memory.SemanticRetriever
}

type memoryReadConfig struct {
	MemoryTypes []string `json:"memory_types"`
	Limit       int      `json:"limit"`
	Query       string   `json:"query"`
}

func (MemoryReadNode) Type() string { return "memory_read" }

func (MemoryReadNode) Validate(config json.RawMessage) error {
	var cfg memoryReadConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("%w: invalid memory_read config", agenterrors.ErrInvalidInput)
	}
	if cfg.Limit < 0 || cfg.Limit > 20 {
		return fmt.Errorf("%w: memory_read limit must be <= 20", agenterrors.ErrInvalidInput)
	}
	return nil
}

func (n MemoryReadNode) Run(ctx context.Context, rc *engine.RunContext, input engine.NodeInput, config json.RawMessage) (engine.NodeOutput, error) {
	if n.Memories == nil {
		return nil, fmt.Errorf("memory repository is not configured")
	}
	var cfg memoryReadConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, err
	}
	emitRuntimeEvent(ctx, rc, runtimeevent.Event{Type: runtimeevent.MemoryReadStarted, RunID: rc.RunID})

	query := strings.TrimSpace(cfg.Query)
	if query != "" && n.Retriever != nil {
		ids, err := n.Retriever.Search(ctx, rc.OwnerID, query, cfg.MemoryTypes, cfg.Limit)
		if err == nil && len(ids) > 0 {
			items, lines, fetchedIDs := fetchMemoriesByIDs(ctx, n.Memories, rc.OwnerID, ids)
			_ = n.Memories.MarkUsed(ctx, rc.OwnerID, fetchedIDs)
			emitRuntimeEvent(ctx, rc, runtimeevent.Event{Type: runtimeevent.MemoryReadFinished, RunID: rc.RunID, Payload: map[string]any{"count": len(items), "query": query}})
			return engine.NodeOutput{"memories": items, "memory_context": strings.Join(lines, "\n"), "count": len(items), "query": query}, nil
		}
	}

	items, err := n.Memories.ListForRead(ctx, rc.OwnerID, cfg.MemoryTypes, rc.ConversationID, cfg.Limit)
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(items))
	lines := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
		lines = append(lines, item.Content)
	}
	_ = n.Memories.MarkUsed(ctx, rc.OwnerID, ids)
	emitRuntimeEvent(ctx, rc, runtimeevent.Event{Type: runtimeevent.MemoryReadFinished, RunID: rc.RunID, Payload: map[string]any{"count": len(items)}})
	return engine.NodeOutput{"memories": items, "memory_context": strings.Join(lines, "\n")}, nil
}

func fetchMemoriesByIDs(ctx context.Context, repo memory.Repository, ownerID int64, ids []int64) ([]memory.Memory, []string, []int64) {
	items := make([]memory.Memory, 0, len(ids))
	lines := make([]string, 0, len(ids))
	fetchedIDs := make([]int64, 0, len(ids))
	for _, id := range ids {
		item, err := repo.FindByID(ctx, ownerID, id)
		if err != nil {
			continue
		}
		items = append(items, *item)
		lines = append(lines, item.Content)
		fetchedIDs = append(fetchedIDs, id)
	}
	return items, lines, fetchedIDs
}

type MemoryWriteNode struct {
	Memories  memory.Repository
	Logs      memory.WriteLogRepository
	Retriever memory.SemanticRetriever
}

type memoryWriteConfig struct {
	MemoryID   int64   `json:"memory_id"`
	MemoryType string  `json:"memory_type"`
	Title      string  `json:"title"`
	Content    string  `json:"content"`
	Importance float64 `json:"importance"`
	Reason     string  `json:"reason"`
	Source     string  `json:"source"`
}

func (MemoryWriteNode) Type() string { return "memory_write" }

func (MemoryWriteNode) Validate(config json.RawMessage) error {
	var cfg memoryWriteConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("%w: invalid memory_write config", agenterrors.ErrInvalidInput)
	}
	if strings.TrimSpace(cfg.MemoryType) == "" {
		return fmt.Errorf("%w: memory_write memory_type is required", agenterrors.ErrInvalidInput)
	}
	if strings.TrimSpace(cfg.Content) == "" {
		return fmt.Errorf("%w: memory_write content is required", agenterrors.ErrInvalidInput)
	}
	if cfg.Importance < 0 || cfg.Importance > 1 {
		return fmt.Errorf("%w: memory_write importance must be between 0 and 1", agenterrors.ErrInvalidInput)
	}
	return nil
}

func (n MemoryWriteNode) Run(ctx context.Context, rc *engine.RunContext, input engine.NodeInput, config json.RawMessage) (engine.NodeOutput, error) {
	if n.Memories == nil {
		return nil, fmt.Errorf("memory repository is not configured")
	}
	var cfg memoryWriteConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, err
	}
	content := strings.TrimSpace(engine.ResolveTemplate(cfg.Content, rc))
	if content == "" {
		return nil, fmt.Errorf("%w: resolved memory content is empty", agenterrors.ErrInvalidInput)
	}
	emitRuntimeEvent(ctx, rc, runtimeevent.Event{Type: runtimeevent.MemoryWriteStarted, RunID: rc.RunID})
	action := memory.WriteActionCreate
	var beforeJSON json.RawMessage
	item := &memory.Memory{}
	if cfg.MemoryID > 0 {
		existing, err := n.Memories.FindByID(ctx, rc.OwnerID, cfg.MemoryID)
		if err != nil {
			return nil, err
		}
		beforeJSON, _ = json.Marshal(existing)
		item = existing
		action = memory.WriteActionUpdate
	} else {
		item.OwnerID = rc.OwnerID
		item.ConversationID = rc.ConversationID
	}
	item.MemoryType = strings.TrimSpace(cfg.MemoryType)
	item.Title = strings.TrimSpace(engine.ResolveTemplate(cfg.Title, rc))
	item.Content = content
	item.Importance = cfg.Importance
	if item.Importance == 0 {
		item.Importance = 0.5
	}
	item.Source = strings.TrimSpace(cfg.Source)
	if item.Source == "" {
		item.Source = "agent"
	}
	var err error
	if action == memory.WriteActionCreate {
		err = n.Memories.Create(ctx, item)
	} else {
		err = n.Memories.Update(ctx, item)
	}
	if err != nil {
		return nil, err
	}
	if item.MemoryLevel == "" {
		item.MemoryLevel = memory.LevelLongTerm
	}
	if n.Retriever != nil {
		if err := n.Retriever.Index(ctx, *item); err != nil {
			return nil, err
		}
	}
	afterJSON, _ := json.Marshal(item)
	if n.Logs != nil {
		_ = n.Logs.Create(ctx, &memory.WriteLog{OwnerID: rc.OwnerID, MemoryID: item.ID, RunID: rc.RunID, Action: action, BeforeJSON: beforeJSON, AfterJSON: afterJSON, Reason: engine.ResolveTemplate(cfg.Reason, rc)})
	}
	emitRuntimeEvent(ctx, rc, runtimeevent.Event{Type: runtimeevent.MemoryWriteFinished, RunID: rc.RunID, Payload: map[string]any{"memory_id": item.ID, "action": action}})
	return engine.NodeOutput{"memory_id": item.ID, "action": action, "content": item.Content}, nil
}
