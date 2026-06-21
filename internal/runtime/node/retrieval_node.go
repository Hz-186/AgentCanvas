package node

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agentcanvas/internal/domain/retrieval"
	"agentcanvas/internal/runtime/engine"
	runtimeevent "agentcanvas/internal/runtime/event"

	agenterrors "agentcanvas/internal/pkg/errors"
)

type RetrievalNode struct {
	Retriever retrieval.Retriever
}

type retrievalConfig struct {
	KBIDs []int64 `json:"kb_ids"`
	TopK  int     `json:"top_k"`
	Mode  string  `json:"mode"`
	Query string  `json:"query"`
}

func (RetrievalNode) Type() string { return "knowledge_retrieval" }

func (RetrievalNode) Validate(config json.RawMessage) error {
	var cfg retrievalConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return fmt.Errorf("%w: invalid retrieval config", agenterrors.ErrInvalidInput)
	}
	if len(cfg.KBIDs) == 0 {
		return fmt.Errorf("%w: retrieval kb_ids are required", agenterrors.ErrInvalidInput)
	}
	if cfg.TopK < 0 || cfg.TopK > 20 {
		return fmt.Errorf("%w: retrieval top_k must be <= 20", agenterrors.ErrInvalidInput)
	}
	if cfg.Mode != "" && cfg.Mode != string(retrieval.ModeKeyword) && cfg.Mode != string(retrieval.ModeVector) && cfg.Mode != string(retrieval.ModeHybrid) {
		return fmt.Errorf("%w: unsupported retrieval mode", agenterrors.ErrInvalidInput)
	}
	return nil
}

func (n RetrievalNode) Run(ctx context.Context, rc *engine.RunContext, input engine.NodeInput, config json.RawMessage) (engine.NodeOutput, error) {
	if n.Retriever == nil {
		return nil, fmt.Errorf("retriever is not configured")
	}
	var cfg retrievalConfig
	if err := json.Unmarshal(config, &cfg); err != nil {
		return nil, err
	}
	query := strings.TrimSpace(engine.ResolveTemplate(cfg.Query, rc))
	if query == "" {
		if value, ok := rc.Input["query"].(string); ok {
			query = value
		}
	}
	if query == "" {
		return nil, fmt.Errorf("%w: retrieval query is required", agenterrors.ErrInvalidInput)
	}
	emitRuntimeEvent(ctx, rc, runtimeevent.Event{Type: runtimeevent.RetrievalStarted, RunID: rc.RunID, NodeType: n.Type()})
	mode := retrieval.Mode(cfg.Mode)
	if mode == "" {
		mode = retrieval.ModeKeyword
	}
	resp, err := n.Retriever.Search(ctx, retrieval.RetrievalRequest{OwnerID: rc.OwnerID, KBIDs: cfg.KBIDs, Query: query, TopK: cfg.TopK, Mode: mode, EnableHighlight: true})
	if err != nil {
		return nil, err
	}
	contexts := make([]string, 0, len(resp.Results))
	for _, item := range resp.Results {
		contexts = append(contexts, item.Content)
	}
	emitRuntimeEvent(ctx, rc, runtimeevent.Event{Type: runtimeevent.RetrievalFinished, RunID: rc.RunID, NodeType: n.Type(), Payload: map[string]any{"result_count": len(resp.Results), "latency_ms": resp.LatencyMS}})
	return engine.NodeOutput{"results": resp.Results, "context": strings.Join(contexts, "\n\n"), "result_count": len(resp.Results), "latency_ms": resp.LatencyMS}, nil
}
