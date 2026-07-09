package toolruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agentcanvas/internal/domain/memory"

	agenterrors "agentcanvas/internal/pkg/errors"
)

type MemoryReadTool struct {
	Memories  memory.Repository
	Retriever memory.SemanticRetriever
}

type memoryReadInput struct {
	MemoryTypes []string `json:"memory_types"`
	Limit       int      `json:"limit"`
	Query       string   `json:"query"`
}

func (MemoryReadTool) Name() string { return "read_memory" }

func (MemoryReadTool) Description() string {
	return "Read long-term memories for this conversation or user. Use before answering when prior preferences, summaries, or task notes may matter."
}

func (MemoryReadTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"memory_types":{"type":"array","items":{"type":"string"},"description":"Memory types to read. Common values: profile_memory, summary_memory, episodic_memory, task_memory, archival_memory."},"limit":{"type":"number","description":"Maximum memories to read. Default 5, maximum 20."},"query":{"type":"string","description":"Semantic search query to find relevant memories. When provided, results are ranked by relevance to this query."}},"additionalProperties":false}`)
}

func (MemoryReadTool) Metadata() ToolMetadata {
	return ToolMetadata{RiskLevel: RiskLow, SideEffect: SideEffectRead}
}

func (t MemoryReadTool) Execute(ctx context.Context, rc ToolRunContext, input json.RawMessage) (*ToolResult, error) {
	if t.Memories == nil {
		return nil, fmt.Errorf("memory repository is not configured")
	}
	var parsed memoryReadInput
	if len(input) > 0 {
		if err := json.Unmarshal(input, &parsed); err != nil {
			return &ToolResult{ContentText: err.Error(), IsError: true}, err
		}
	}
	limit := parsed.Limit
	if limit <= 0 {
		limit = 5
	}
	if limit > 20 {
		limit = 20
	}

	query := strings.TrimSpace(parsed.Query)
	if query != "" && t.Retriever != nil {
		ids, err := t.Retriever.Search(ctx, rc.OwnerID, query, parsed.MemoryTypes, limit)
		if err == nil && len(ids) > 0 {
			return t.fetchByIDs(ctx, rc, ids, query)
		}
	}

	items, err := t.Memories.ListForRead(ctx, rc.OwnerID, parsed.MemoryTypes, rc.ConversationID, limit)
	if err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	ids := make([]int64, 0, len(items))
	lines := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
		lines = append(lines, item.Content)
	}
	fetchedIDs := make([]int64, 0, len(items))
	for _, item := range items {
		fetchedIDs = append(fetchedIDs, item.ID)
	}
	_ = t.Memories.MarkUsed(ctx, rc.OwnerID, fetchedIDs)
	return ResultFromValue(map[string]any{
		"memories":       items,
		"memory_context": strings.Join(lines, "\n"),
		"count":          len(items),
	})
}

func (t MemoryReadTool) fetchByIDs(ctx context.Context, rc ToolRunContext, ids []int64, query string) (*ToolResult, error) {
	items := make([]memory.Memory, 0, len(ids))
	lines := make([]string, 0, len(ids))
	for _, id := range ids {
		item, err := t.Memories.FindByID(ctx, rc.OwnerID, id)
		if err != nil {
			continue
		}
		items = append(items, *item)
		lines = append(lines, item.Content)
	}
	_ = t.Memories.MarkUsed(ctx, rc.OwnerID, ids)
	return ResultFromValue(map[string]any{
		"memories":       items,
		"memory_context": strings.Join(lines, "\n"),
		"count":          len(items),
		"query":          query,
	})
}

type MemoryWriteTool struct {
	Memories  memory.Repository
	Logs      memory.WriteLogRepository
	Retriever memory.SemanticRetriever
}

type memoryWriteInput struct {
	MemoryID   int64   `json:"memory_id"`
	MemoryType string  `json:"memory_type"`
	Title      string  `json:"title"`
	Content    string  `json:"content"`
	Importance float64 `json:"importance"`
	Reason     string  `json:"reason"`
}

func (MemoryWriteTool) Name() string { return "write_memory" }

func (MemoryWriteTool) Description() string {
	return "Create or update a long-term memory when the user gives stable preferences, durable facts, task summaries, or reusable context."
}

func (MemoryWriteTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"memory_id":{"type":"number","description":"Existing memory ID to update. Omit or use 0 to create."},"memory_type":{"type":"string","enum":["profile_memory","summary_memory","episodic_memory","task_memory","archival_memory"]},"title":{"type":"string"},"content":{"type":"string","description":"Durable memory content to store."},"importance":{"type":"number","description":"0 to 1. Defaults to 0.5."},"reason":{"type":"string","description":"Why this memory should be stored."}},"required":["memory_type","content"],"additionalProperties":false}`)
}

func (MemoryWriteTool) Metadata() ToolMetadata {
	return ToolMetadata{RiskLevel: RiskMedium, SideEffect: SideEffectWrite}
}

func (t MemoryWriteTool) Execute(ctx context.Context, rc ToolRunContext, input json.RawMessage) (*ToolResult, error) {
	if t.Memories == nil {
		return nil, fmt.Errorf("memory repository is not configured")
	}
	var parsed memoryWriteInput
	if err := json.Unmarshal(input, &parsed); err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	content := strings.TrimSpace(parsed.Content)
	if content == "" || strings.TrimSpace(parsed.MemoryType) == "" {
		return &ToolResult{ContentText: "memory_type and content are required", IsError: true}, fmt.Errorf("%w: memory_type and content are required", agenterrors.ErrInvalidInput)
	}
	action := memory.WriteActionCreate
	var beforeJSON json.RawMessage
	item := &memory.Memory{}
	if parsed.MemoryID > 0 {
		existing, err := t.Memories.FindByID(ctx, rc.OwnerID, parsed.MemoryID)
		if err != nil {
			return &ToolResult{ContentText: err.Error(), IsError: true}, err
		}
		beforeJSON, _ = json.Marshal(existing)
		item = existing
		action = memory.WriteActionUpdate
	} else {
		item.OwnerID = rc.OwnerID
		item.ConversationID = rc.ConversationID
	}
	item.MemoryType = strings.TrimSpace(parsed.MemoryType)
	item.Title = strings.TrimSpace(parsed.Title)
	item.Content = content
	item.Importance = parsed.Importance
	if item.Importance <= 0 {
		item.Importance = 0.5
	}
	if item.Importance > 1 {
		item.Importance = 1
	}
	item.Source = "agent_tool"
	var err error
	if action == memory.WriteActionCreate {
		err = t.Memories.Create(ctx, item)
	} else {
		err = t.Memories.Update(ctx, item)
	}
	if err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	if item.MemoryLevel == "" {
		item.MemoryLevel = memory.LevelLongTerm
	}
	if t.Retriever != nil {
		if err := t.Retriever.Index(ctx, *item); err != nil {
			return &ToolResult{ContentText: err.Error(), IsError: true}, err
		}
	}
	afterJSON, _ := json.Marshal(item)
	if t.Logs != nil {
		_ = t.Logs.Create(ctx, &memory.WriteLog{
			OwnerID:    rc.OwnerID,
			MemoryID:   item.ID,
			RunID:      rc.RunID,
			Action:     action,
			BeforeJSON: beforeJSON,
			AfterJSON:  afterJSON,
			Reason:     strings.TrimSpace(parsed.Reason),
		})
	}
	return ResultFromValue(map[string]any{
		"memory_id": item.ID,
		"action":    action,
		"content":   item.Content,
	})
}
