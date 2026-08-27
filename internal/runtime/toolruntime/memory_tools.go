package toolruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agentcanvas/internal/domain/contextresource"
	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/memory"
	"agentcanvas/internal/observability"
)

type SessionSearchTool struct {
	Index conversation.MessageSearchIndex
}

func (SessionSearchTool) Name() string { return "search_sessions" }
func (SessionSearchTool) Description() string {
	return "Search this Agent's prior conversations for relevant historical messages without loading all session history into context."
}
func (SessionSearchTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"conversation_id":{"type":"number"},"from":{"type":"string","description":"RFC3339 lower time bound"},"to":{"type":"string","description":"RFC3339 upper time bound"},"limit":{"type":"number","minimum":1,"maximum":50}},"required":["query"],"additionalProperties":false}`)
}
func (SessionSearchTool) Metadata() ToolMetadata {
	return ToolMetadata{RiskLevel: RiskLow, SideEffect: SideEffectRead}
}
func (t SessionSearchTool) Execute(ctx context.Context, rc ToolRunContext, input json.RawMessage) (*ToolResult, error) {
	observability.ContextSystemMetrics.RecordHistorySearch()
	if t.Index == nil || rc.AgentID <= 0 {
		return nil, fmt.Errorf("session search is not configured")
	}
	var parsed struct {
		Query          string `json:"query"`
		ConversationID int64  `json:"conversation_id"`
		From           string `json:"from"`
		To             string `json:"to"`
		Limit          int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &parsed); err != nil {
		return nil, err
	}
	request := conversation.MessageSearchRequest{OwnerID: rc.OwnerID, AgentID: rc.AgentID, Query: strings.TrimSpace(parsed.Query), Limit: parsed.Limit}
	if parsed.ConversationID > 0 {
		request.ConversationID = &parsed.ConversationID
	}
	if strings.TrimSpace(parsed.From) != "" {
		value, err := time.Parse(time.RFC3339, parsed.From)
		if err != nil {
			return nil, fmt.Errorf("from must be RFC3339")
		}
		request.From = &value
	}
	if strings.TrimSpace(parsed.To) != "" {
		value, err := time.Parse(time.RFC3339, parsed.To)
		if err != nil {
			return nil, fmt.Errorf("to must be RFC3339")
		}
		request.To = &value
	}
	items, err := t.Index.SearchMessages(ctx, request)
	if err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	return ResultFromValue(map[string]any{"query": request.Query, "count": len(items), "messages": items})
}

type MemoryReadTool struct {
	// Files is the file-backed durable memory reader. When configured it is the
	// only Agent-facing memory surface; the SQL repository fields below exist
	// solely for old maintenance/test integrations.
	Files        memory.DurableReader
	Memories     memory.Repository
	RecallLogs   memory.RecallLogRepository
	Retriever    memory.SemanticRetriever
	Archival     memory.ArchivalIndex
	ContextIndex contextresource.Index
	AgentID      int64
	Profile      contextresource.EmbeddingProfile
	TokenBudget  int
	// AllowLegacyListFallback is only for old integrations. New Agent Runtime
	// wiring leaves it false so memory reads remain semantic-only.
	AllowLegacyListFallback bool
}

type memoryReadInput struct {
	Limit int    `json:"limit"`
	Query string `json:"query"`
}

func (MemoryReadTool) Name() string { return "read_memory" }

func (t MemoryReadTool) Description() string {
	if t.Files != nil {
		return "Search durable memory files on demand. Use the query to find relevant entries in MEMORY.md, rollout summaries, or reusable skills."
	}
	return "Search durable memory on demand. The legacy repository is retained only for migration and is not the production memory surface."
}

func (t MemoryReadTool) Parameters() json.RawMessage {
	// One Agent-facing schema for both the file reader and migration fallback.
	// Storage taxonomy (profile/episodic/task/archival, short/long) is never a
	// model-selectable dimension.
	return json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"Search query for durable memory."},"limit":{"type":"number","description":"Maximum entries to return. Default 5, maximum 20."}},"required":["query"],"additionalProperties":false}`)
}

func (MemoryReadTool) Metadata() ToolMetadata {
	return ToolMetadata{RiskLevel: RiskLow, SideEffect: SideEffectRead}
}

func (t MemoryReadTool) Execute(ctx context.Context, rc ToolRunContext, input json.RawMessage) (*ToolResult, error) {
	if t.Files != nil {
		var parsed struct {
			Query string `json:"query"`
			Limit int    `json:"limit"`
		}
		if len(input) > 0 {
			decoder := json.NewDecoder(strings.NewReader(string(input)))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&parsed); err != nil {
				return &ToolResult{ContentText: err.Error(), IsError: true}, err
			}
		}
		query := strings.TrimSpace(parsed.Query)
		if query == "" {
			query = strings.TrimSpace(rc.Task)
		}
		if query == "" {
			return nil, fmt.Errorf("durable memory search query is required")
		}
		items, err := t.Files.Search(ctx, rc.OwnerID, query, parsed.Limit)
		if err != nil {
			return &ToolResult{ContentText: err.Error(), IsError: true}, err
		}
		return ResultFromValue(map[string]any{"query": query, "count": len(items), "files": items})
	}
	if t.Memories == nil {
		return nil, fmt.Errorf("memory repository is not configured")
	}
	if t.ContextIndex == nil && t.Retriever == nil && !t.AllowLegacyListFallback {
		return nil, fmt.Errorf("unified vector memory index is not configured")
	}
	var parsed memoryReadInput
	if len(input) > 0 {
		if err := json.Unmarshal(input, &parsed); err != nil {
			return &ToolResult{ContentText: err.Error(), IsError: true}, err
		}
	}
	query := strings.TrimSpace(parsed.Query)
	semanticOnly := !t.AllowLegacyListFallback
	allowLegacyFallback := t.AllowLegacyListFallback
	if query == "" {
		query = strings.TrimSpace(rc.Task)
	}
	agentID := t.AgentID
	if agentID == 0 {
		agentID = rc.AgentID
	}
	projectID := projectIDFromToolRunContext(rc)
	result, err := (memory.RuntimeService{Memories: t.Memories, RecallLogs: t.RecallLogs, Retriever: t.Retriever, Archival: t.Archival, ContextIndex: t.ContextIndex, AgentID: agentID, Profile: t.Profile}).Read(ctx, memory.ReadRequest{
		OwnerID: rc.OwnerID, ConversationID: rc.ConversationID, ProjectID: projectID, AgentID: agentID, RunID: rc.RunID, Query: query, Limit: parsed.Limit, TokenBudget: t.TokenBudget, SemanticOnly: semanticOnly, AllowLegacyListFallback: allowLegacyFallback,
	})
	if err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	return ResultFromValue(map[string]any{
		"memories": result.Memories, "memory_context": result.MemoryContext, "count": result.Count, "query": result.Query, "recall_details": result.RecallDetails,
	})
}

func projectIDFromToolRunContext(rc ToolRunContext) int64 {
	if rc.ProjectID > 0 {
		return rc.ProjectID
	}
	if rc.Workspace != nil {
		return rc.Workspace.ProjectID
	}
	return 0
}
