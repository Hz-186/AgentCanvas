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
	MemoryTypes []string `json:"memory_types"`
	Limit       int      `json:"limit"`
	Query       string   `json:"query"`
}

func (MemoryReadTool) Name() string { return "read_memory" }

func (MemoryReadTool) Description() string {
	return "Semantically retrieve only memories relevant to the current task. Reads both short-term and long-term memory without loading a recent-memory list."
}

func (MemoryReadTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"memory_types":{"type":"array","items":{"type":"string"},"description":"Memory types to read: profile, episodic, task, archival."},"limit":{"type":"number","description":"Maximum memories to read. Default 5, maximum 20."},"query":{"type":"string","description":"Semantic search query to find relevant memories. When provided, results are ranked by relevance to this query."}},"additionalProperties":false}`)
}

func (MemoryReadTool) Metadata() ToolMetadata {
	return ToolMetadata{RiskLevel: RiskLow, SideEffect: SideEffectRead}
}

func (t MemoryReadTool) Execute(ctx context.Context, rc ToolRunContext, input json.RawMessage) (*ToolResult, error) {
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
		OwnerID: rc.OwnerID, ConversationID: rc.ConversationID, ProjectID: projectID, AgentID: agentID, RunID: rc.RunID, MemoryTypes: parsed.MemoryTypes, Query: query, Limit: parsed.Limit, TokenBudget: t.TokenBudget, SemanticOnly: semanticOnly, AllowLegacyListFallback: allowLegacyFallback,
	})
	if err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	return ResultFromValue(map[string]any{
		"memories": result.Memories, "memory_context": result.MemoryContext, "count": result.Count, "query": result.Query, "recall_details": result.RecallDetails,
	})
}

type MemoryWriteTool struct {
	Memories   memory.Repository
	Logs       memory.WriteLogRepository
	Retriever  memory.SemanticRetriever
	Archival   memory.ArchivalIndex
	Candidates memory.CandidateWriter
}

type memoryWriteInput struct {
	MemoryID           int64   `json:"memory_id"`
	MemoryType         string  `json:"memory_type"`
	Title              string  `json:"title"`
	Content            string  `json:"content"`
	Importance         float64 `json:"importance"`
	Reason             string  `json:"reason"`
	ConflictResolution string  `json:"conflict_resolution"`
	Scope              string  `json:"scope"`
}

func (MemoryWriteTool) Name() string { return "write_memory" }

func (MemoryWriteTool) Description() string {
	return "Create or update a long-term memory when the user gives stable preferences, durable facts, task summaries, or reusable context."
}

func (MemoryWriteTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"memory_id":{"type":"number","description":"Existing memory ID to update. Omit or use 0 to create."},"memory_type":{"type":"string","enum":["profile","episodic","task","archival"]},"title":{"type":"string"},"content":{"type":"string","description":"Durable memory content to propose for review."},"importance":{"type":"number","description":"0 to 1. Defaults to 0.5."},"reason":{"type":"string","description":"Why this memory should be stored."},"scope":{"type":"string","enum":["user","agent","project","conversation"],"description":"Optional memory scope. Defaults by memory type and current project or conversation."}},"required":["memory_type","content"],"additionalProperties":false}`)
}

func (MemoryWriteTool) Metadata() ToolMetadata {
	return ToolMetadata{RiskLevel: RiskMedium, SideEffect: SideEffectWrite}
}

func (t MemoryWriteTool) Execute(ctx context.Context, rc ToolRunContext, input json.RawMessage) (*ToolResult, error) {
	if t.Candidates == nil {
		return nil, fmt.Errorf("memory candidate service is not configured")
	}
	var parsed memoryWriteInput
	if err := json.Unmarshal(input, &parsed); err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	conversationID := int64(0)
	if rc.ConversationID != nil {
		conversationID = *rc.ConversationID
	}
	projectID := projectIDFromToolRunContext(rc)
	action := "create"
	if parsed.MemoryID > 0 {
		action = "update"
	}
	proposalID, err := t.Candidates.Suggest(ctx, memory.CandidateRequest{OwnerID: rc.OwnerID, AgentID: rc.AgentID,
		ConversationID: conversationID, ProjectID: projectID, SourceConversationID: conversationID, SourceProjectID: projectID, RunID: rc.RunID, ScopeType: strings.TrimSpace(parsed.Scope), SourceID: fmt.Sprintf("agent-tool:%d:%s", rc.RunID, strings.TrimSpace(parsed.Content)),
		MemoryID: parsed.MemoryID, MemoryType: parsed.MemoryType, Title: parsed.Title, Content: parsed.Content,
		Action: action, Importance: parsed.Importance, Evidence: []string{strings.TrimSpace(parsed.Reason)}, Source: "agent_tool"})
	if err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	return ResultFromValue(map[string]any{
		"proposal_id": proposalID, "status": "pending", "action": "suggest", "content": strings.TrimSpace(parsed.Content),
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
