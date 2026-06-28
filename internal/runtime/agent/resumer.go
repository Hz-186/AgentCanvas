package agent

import (
	"encoding/json"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/infrastructure/llm"
	"agentcanvas/internal/runtime/toolruntime"
)

type ResumeRequest struct {
	OwnerID            int64
	AgentID            int64
	RunID              int64
	NodeID             string
	CallDepth          int
	CallChain          []int64
	ConversationID     *int64
	Provider           llm.ChatProviderConfig
	Model              string
	Mode               string
	SystemPrompt       string
	Task               string
	ReflectionEnabled  bool
	Temperature        *float64
	MaxIterations      int
	MaxToolCalls       int
	MaxExecutionTimeMS int
	MaxInputChars      int
	ContextBlocks      []ContextBlock
	ToolPolicy         ToolPolicy
	Tools              []toolruntime.RuntimeTool
	Checkpoint         *Checkpoint
	Approved           bool
	RejectionNote      string
}

func BuildResumeRequest(req ResumeRequest) (*RunRequest, error) {
	messages := req.Checkpoint.Messages
	iteration := 0
	toolCalls := 0
	if v, ok := req.Checkpoint.Metadata["iteration"]; ok {
		if i, ok := v.(float64); ok {
			iteration = int(i)
		}
	}
	if v, ok := req.Checkpoint.Metadata["tool_calls"]; ok {
		if i, ok := v.(float64); ok {
			toolCalls = int(i)
		}
	}
	if !req.Approved && req.Checkpoint.PendingToolCall != nil {
		rejectionContent := "Human rejected the request to execute tool " + req.Checkpoint.PendingToolCall.Name
		if req.RejectionNote != "" {
			rejectionContent = "Human rejected: " + req.RejectionNote
		}
		messages = append(messages, llm.ChatMessage{
			Role:       conversation.RoleTool,
			ToolCallID: req.Checkpoint.PendingToolCall.ID,
			Content:    rejectionContent,
		})
		pending := req.Checkpoint.PendingToolCall
		_ = pending
	}
	contextBlocks := req.ContextBlocks
	if len(contextBlocks) == 0 {
		contextBlocks = nil
	}
	return &RunRequest{
		OwnerID:            req.OwnerID,
		AgentID:            req.AgentID,
		RunID:              req.RunID,
		NodeID:             req.NodeID,
		CallDepth:          req.CallDepth,
		CallChain:          req.CallChain,
		ConversationID:     req.ConversationID,
		Provider:           req.Provider,
		Model:              req.Model,
		Mode:               req.Mode,
		SystemPrompt:       req.SystemPrompt,
		Task:               req.Task,
		ReflectionEnabled:  req.ReflectionEnabled,
		Temperature:        req.Temperature,
		MaxIterations:      req.MaxIterations,
		MaxToolCalls:       req.MaxToolCalls,
		MaxExecutionTimeMS: req.MaxExecutionTimeMS,
		MaxInputChars:      req.MaxInputChars,
		ContextBlocks:      contextBlocks,
		ToolPolicy:         req.ToolPolicy,
		Tools:              req.Tools,
		ResumeMessages:     messages,
		ResumeIteration:    iteration,
		ResumeToolCalls:    toolCalls,
	}, nil
}

func CheckpointFromJSON(data json.RawMessage) (*Checkpoint, error) {
	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, err
	}
	return &cp, nil
}
