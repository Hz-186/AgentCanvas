package toolruntime

import (
	"context"
	"encoding/json"
	"fmt"

	"agentcanvas/internal/domain/memory"
)

type MemoryReadTool struct {
	Memories  memory.Repository
	Retriever memory.SemanticRetriever
	Archival  memory.ArchivalIndex
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
	result, err := (memory.RuntimeService{Memories: t.Memories, Retriever: t.Retriever, Archival: t.Archival}).Read(ctx, memory.ReadRequest{
		OwnerID: rc.OwnerID, ConversationID: rc.ConversationID, MemoryTypes: parsed.MemoryTypes, Query: parsed.Query, Limit: parsed.Limit,
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
	result, err := (memory.RuntimeService{Memories: t.Memories, Logs: t.Logs, Retriever: t.Retriever, Archival: t.Archival}).Write(ctx, memory.WriteRequest{
		OwnerID: rc.OwnerID, ConversationID: rc.ConversationID, RunID: rc.RunID, MemoryID: parsed.MemoryID,
		MemoryType: parsed.MemoryType, Title: parsed.Title, Content: parsed.Content, Importance: parsed.Importance, Reason: parsed.Reason, Source: "agent_tool",
	})
	if err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	return ResultFromValue(map[string]any{
		"memory_id": result.Memory.ID, "action": result.Action, "content": result.Memory.Content,
	})
}
