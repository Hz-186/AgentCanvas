package toolruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/memory"
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
	Memories  memory.Repository
	Retriever memory.SemanticRetriever
	Archival  memory.ArchivalIndex
	// AllowLegacyListFallback is only for old integrations. New Agent Runtime
	// wiring leaves it false so memory reads remain semantic-only.
	AllowLegacyListFallback bool
}

type memoryReadInput struct {
	MemoryTypes []string `json:"memory_types"`
	Limit       int      `json:"limit"`
	Query       string   `json:"query"`
}

func (MemoryReadTool) Name() string { return "read_memory" }

func (MemoryReadTool) Description() string {
	return "Semantically retrieve only memories relevant to the current task. Reads both short-term and long-term memory without loading a recent-memory list."
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
	if t.Retriever == nil && !t.AllowLegacyListFallback {
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
	result, err := (memory.RuntimeService{Memories: t.Memories, Retriever: t.Retriever, Archival: t.Archival}).Read(ctx, memory.ReadRequest{
		OwnerID: rc.OwnerID, ConversationID: rc.ConversationID, MemoryTypes: parsed.MemoryTypes, Query: query, Limit: parsed.Limit, SemanticOnly: semanticOnly, AllowLegacyListFallback: allowLegacyFallback,
	})
	if err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	return ResultFromValue(map[string]any{
		"memories": result.Memories, "memory_context": result.MemoryContext, "count": result.Count, "query": result.Query,
	})
}

type MemoryWriteTool struct {
	Memories  memory.Repository
	Logs      memory.WriteLogRepository
	Retriever memory.SemanticRetriever
	Archival  memory.ArchivalIndex
}

type memoryWriteInput struct {
	MemoryID           int64   `json:"memory_id"`
	MemoryType         string  `json:"memory_type"`
	Title              string  `json:"title"`
	Content            string  `json:"content"`
	Importance         float64 `json:"importance"`
	Reason             string  `json:"reason"`
	ConflictResolution string  `json:"conflict_resolution"`
}

func (MemoryWriteTool) Name() string { return "write_memory" }

func (MemoryWriteTool) Description() string {
	return "Create or update a long-term memory when the user gives stable preferences, durable facts, task summaries, or reusable context."
}

func (MemoryWriteTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"memory_id":{"type":"number","description":"Existing memory ID to update. Omit or use 0 to create."},"memory_type":{"type":"string","enum":["profile_memory","summary_memory","episodic_memory","task_memory","archival_memory"]},"title":{"type":"string"},"content":{"type":"string","description":"Durable memory content to store."},"importance":{"type":"number","description":"0 to 1. Defaults to 0.5."},"reason":{"type":"string","description":"Why this memory should be stored."},"conflict_resolution":{"type":"string","description":"Injected by the runtime after user approval; do not set proactively."}},"required":["memory_type","content"],"additionalProperties":false}`)
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
	result, err := (memory.RuntimeService{Memories: t.Memories, Logs: t.Logs, Retriever: t.Retriever, Archival: t.Archival}).Write(ctx, memory.WriteRequest{
		OwnerID: rc.OwnerID, ConversationID: rc.ConversationID, RunID: rc.RunID, MemoryID: parsed.MemoryID,
		MemoryType: parsed.MemoryType, Title: parsed.Title, Content: parsed.Content, Importance: parsed.Importance, Reason: parsed.Reason, Source: "agent_tool",
		ConflictResolution: parsed.ConflictResolution,
	})
	if err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	if result.Conflict != nil {
		options := make([]ApprovalOption, 0, len(result.Conflict.Options))
		for _, option := range result.Conflict.Options {
			options = append(options, ApprovalOption{ID: option.ID, Label: option.Label, Description: option.Description})
		}
		return &ToolResult{Approval: &ToolApproval{Kind: "memory_conflict", Title: "记忆冲突需要确认",
			Reason: "新记忆与已有记忆表达了不一致的信息，请选择后续采用哪一种。", Options: options}}, nil
	}
	return ResultFromValue(map[string]any{
		"memory_id": result.Memory.ID, "action": result.Action, "content": result.Memory.Content,
	})
}
