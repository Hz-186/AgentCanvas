package toolruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/infrastructure/vectorstore"

	"github.com/google/uuid"
)

const archivalMemoryCollection = "agent_archival_memories"

type SearchArchivalMemoryTool struct {
	Memories   memory.Repository
	VecStore   vectorstore.Store
	Embedder   llm.EmbeddingClient
	EmbedCfg   llm.EmbeddingProviderConfig
	EmbedModel string
}

type InsertArchivalMemoryTool struct {
	Memories   memory.Repository
	VecStore   vectorstore.Store
	Embedder   llm.EmbeddingClient
	EmbedCfg   llm.EmbeddingProviderConfig
	EmbedModel string
}

type archivalSearchInput struct {
	Query string `json:"query"`
	Limit int    `json:"limit"`
}

type archivalInsertInput struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Reason  string `json:"reason"`
}

func (SearchArchivalMemoryTool) Name() string { return "search_archival_memory" }

func (SearchArchivalMemoryTool) Description() string {
	return "Search long-term archival memory when you need past facts, prior discussions, or cold context not present in the active prompt."
}

func (SearchArchivalMemoryTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"What to recall from archival memory."},"limit":{"type":"number","description":"Maximum results to return. Default 5, maximum 10."}},"required":["query"],"additionalProperties":false}`)
}

func (SearchArchivalMemoryTool) Metadata() ToolMetadata {
	return ToolMetadata{RiskLevel: RiskLow, SideEffect: SideEffectRead}
}

func (t SearchArchivalMemoryTool) Execute(ctx context.Context, rc ToolRunContext, input json.RawMessage) (*ToolResult, error) {
	var parsed archivalSearchInput
	if err := json.Unmarshal(input, &parsed); err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	query := strings.TrimSpace(parsed.Query)
	if query == "" {
		return &ToolResult{ContentText: "query is required", IsError: true}, fmt.Errorf("query is required")
	}
	limit := parsed.Limit
	if limit <= 0 {
		limit = 5
	}
	if limit > 10 {
		limit = 10
	}
	if t.VecStore != nil && t.Embedder != nil && strings.TrimSpace(t.EmbedModel) != "" {
		if result, err := t.searchVector(ctx, rc, query, limit); err == nil {
			return result, nil
		}
	}
	return t.searchFallback(ctx, rc, query, limit)
}

func (t SearchArchivalMemoryTool) searchVector(ctx context.Context, rc ToolRunContext, query string, limit int) (*ToolResult, error) {
	resp, err := t.Embedder.Embed(ctx, t.EmbedCfg, llm.EmbeddingRequest{Model: t.EmbedModel, Input: []string{query}})
	if err != nil {
		return nil, err
	}
	if resp == nil || len(resp.Embeddings) == 0 || len(resp.Embeddings[0]) == 0 {
		return nil, fmt.Errorf("embedding response is empty")
	}
	results, err := t.VecStore.Search(ctx, vectorstore.SearchRequest{
		Collection: archivalMemoryCollection,
		Vector:     resp.Embeddings[0],
		TopK:       limit,
		Filter:     map[string]any{"owner_id": rc.OwnerID},
	})
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(results))
	lines := make([]string, 0, len(results))
	for _, item := range results {
		content, _ := item.Metadata["content"].(string)
		if strings.TrimSpace(content) == "" {
			continue
		}
		items = append(items, map[string]any{"id": item.ID, "score": item.Score, "content": content})
		lines = append(lines, content)
	}
	return ResultFromValue(map[string]any{"query": query, "count": len(items), "archival_memories": items, "memory_context": strings.Join(lines, "\n")})
}

func (t SearchArchivalMemoryTool) searchFallback(ctx context.Context, rc ToolRunContext, query string, limit int) (*ToolResult, error) {
	if t.Memories == nil {
		return nil, fmt.Errorf("memory repository is not configured")
	}
	items, err := t.Memories.List(ctx, rc.OwnerID, []string{memory.TypeArchival}, rc.ConversationID, 50, 0)
	if err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	type scoredMemory struct {
		memory memory.Memory
		score  int
	}
	scored := make([]scoredMemory, 0, len(items))
	needle := strings.ToLower(query)
	for _, item := range items {
		content := strings.ToLower(strings.TrimSpace(item.Content))
		score := archivalContentScore(needle, content)
		if score == 0 {
			continue
		}
		scored = append(scored, scoredMemory{memory: item, score: score})
	}
	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].memory.UpdatedAt.After(scored[j].memory.UpdatedAt)
		}
		return scored[i].score > scored[j].score
	})
	if len(scored) > limit {
		scored = scored[:limit]
	}
	results := make([]memory.Memory, 0, len(scored))
	lines := make([]string, 0, len(scored))
	for _, item := range scored {
		results = append(results, item.memory)
		lines = append(lines, item.memory.Content)
	}
	return ResultFromValue(map[string]any{"query": query, "count": len(results), "archival_memories": results, "memory_context": strings.Join(lines, "\n"), "fallback": "memory_table_scan"})
}

func (InsertArchivalMemoryTool) Name() string { return "insert_archival_memory" }

func (InsertArchivalMemoryTool) Description() string {
	return "Archive durable knowledge or cold facts into long-term archival memory for future retrieval."
}

func (InsertArchivalMemoryTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"},"content":{"type":"string","description":"Durable content to archive."},"reason":{"type":"string","description":"Why this should be archived."}},"required":["content"],"additionalProperties":false}`)
}

func (InsertArchivalMemoryTool) Metadata() ToolMetadata {
	return ToolMetadata{RiskLevel: RiskMedium, SideEffect: SideEffectWrite}
}

func (t InsertArchivalMemoryTool) Execute(ctx context.Context, rc ToolRunContext, input json.RawMessage) (*ToolResult, error) {
	if t.Memories == nil {
		return nil, fmt.Errorf("memory repository is not configured")
	}
	var parsed archivalInsertInput
	if err := json.Unmarshal(input, &parsed); err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	content := strings.TrimSpace(parsed.Content)
	if content == "" {
		return &ToolResult{ContentText: "content is required", IsError: true}, fmt.Errorf("content is required")
	}
	item := &memory.Memory{OwnerID: rc.OwnerID, ConversationID: rc.ConversationID, MemoryType: memory.TypeArchival, MemoryLevel: memory.LevelLongTerm, Title: strings.TrimSpace(parsed.Title), Content: content, Importance: 0.8, Source: "archival_tool"}
	if err := t.Memories.Create(ctx, item); err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	vectorIndexed := false
	if t.VecStore != nil && t.Embedder != nil && strings.TrimSpace(t.EmbedModel) != "" {
		resp, err := t.Embedder.Embed(ctx, t.EmbedCfg, llm.EmbeddingRequest{Model: t.EmbedModel, Input: []string{content}})
		if err == nil && resp != nil && len(resp.Embeddings) > 0 && len(resp.Embeddings[0]) > 0 {
			_ = t.VecStore.EnsureCollection(ctx, archivalMemoryCollection, len(resp.Embeddings[0]), vectorstore.DefaultHNSWConfig())
			if err := t.VecStore.Upsert(ctx, archivalMemoryCollection, []vectorstore.VectorDocument{{ID: uuid.NewString(), Vector: resp.Embeddings[0], Metadata: map[string]any{"owner_id": rc.OwnerID, "conversation_id": conversationIDValue(rc.ConversationID), "memory_id": item.ID, "content": content}}}); err == nil {
				vectorIndexed = true
			}
		}
	}
	return ResultFromValue(map[string]any{"memory_id": item.ID, "content": item.Content, "vector_indexed": vectorIndexed})
}

func archivalContentScore(query, content string) int {
	if query == "" || content == "" {
		return 0
	}
	if strings.Contains(content, query) {
		return 100 + len(query)
	}
	score := 0
	for _, token := range strings.Fields(query) {
		if len(token) >= 2 && strings.Contains(content, token) {
			score += len(token)
		}
	}
	return score
}

func conversationIDValue(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}
