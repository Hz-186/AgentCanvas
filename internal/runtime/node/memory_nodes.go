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
	Archival  memory.ArchivalIndex
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

	result, err := (memory.RuntimeService{Memories: n.Memories, Retriever: n.Retriever, Archival: n.Archival}).Read(ctx, memory.ReadRequest{
		OwnerID: rc.OwnerID, ConversationID: rc.ConversationID, MemoryTypes: cfg.MemoryTypes,
		Query: firstMemoryQuery(cfg.Query, rc), Limit: cfg.Limit, SemanticOnly: true,
	})
	if err != nil {
		return nil, err
	}
	emitRuntimeEvent(ctx, rc, runtimeevent.Event{Type: runtimeevent.MemoryReadFinished, RunID: rc.RunID, Payload: map[string]any{"count": result.Count, "query": result.Query}})
	return engine.NodeOutput{"memories": result.Memories, "memory_context": result.MemoryContext, "count": result.Count, "query": result.Query}, nil
}

func firstMemoryQuery(configured string, rc *engine.RunContext) string {
	if value := strings.TrimSpace(engine.ResolveTemplate(configured, rc)); value != "" {
		return value
	}
	if rc != nil {
		for _, key := range []string{"query", "prompt", "content"} {
			if value, ok := rc.Input[key].(string); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	}
	return ""
}

type MemoryWriteNode struct {
	Memories  memory.Repository
	Logs      memory.WriteLogRepository
	Retriever memory.SemanticRetriever
	Archival  memory.ArchivalIndex
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
	result, err := (memory.RuntimeService{Memories: n.Memories, Logs: n.Logs, Retriever: n.Retriever, Archival: n.Archival}).Write(ctx, memory.WriteRequest{
		OwnerID: rc.OwnerID, ConversationID: rc.ConversationID, RunID: rc.RunID, MemoryID: cfg.MemoryID,
		MemoryType: cfg.MemoryType, Title: engine.ResolveTemplate(cfg.Title, rc), Content: content, Importance: cfg.Importance,
		Reason: engine.ResolveTemplate(cfg.Reason, rc), Source: cfg.Source,
	})
	if err != nil {
		return nil, err
	}
	emitRuntimeEvent(ctx, rc, runtimeevent.Event{Type: runtimeevent.MemoryWriteFinished, RunID: rc.RunID, Payload: map[string]any{"memory_id": result.Memory.ID, "action": result.Action}})
	return engine.NodeOutput{"memory_id": result.Memory.ID, "action": result.Action, "content": result.Memory.Content}, nil
}
