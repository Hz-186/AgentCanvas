package toolruntime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"agentcanvas/internal/domain/retrieval"

	agenterrors "agentcanvas/internal/pkg/errors"
)

type KnowledgeSearchTool struct {
	Retriever retrieval.Retriever
	KBIDs     []int64
	DefaultK  int
	Mode      retrieval.Mode
}

type knowledgeSearchInput struct {
	Query string `json:"query"`
	TopK  int    `json:"top_k"`
	Mode  string `json:"mode"`
}

func (KnowledgeSearchTool) Name() string { return "search_knowledge" }

func (KnowledgeSearchTool) Description() string {
	return "Search the configured knowledge bases for factual context. Use this before answering domain-specific questions or when current context is insufficient."
}

func (KnowledgeSearchTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"A concise semantic search query."},"top_k":{"type":"number","description":"Number of chunks to retrieve. Use 3 to 8 for most cases."},"mode":{"type":"string","enum":["keyword","vector","hybrid"],"description":"Retrieval mode. Defaults to keyword."}},"required":["query"],"additionalProperties":false}`)
}

func (KnowledgeSearchTool) Metadata() ToolMetadata {
	return ToolMetadata{RiskLevel: RiskLow, SideEffect: SideEffectRead}
}

func (t KnowledgeSearchTool) Execute(ctx context.Context, rc ToolRunContext, input json.RawMessage) (*ToolResult, error) {
	if t.Retriever == nil {
		return nil, fmt.Errorf("retriever is not configured")
	}
	if len(t.KBIDs) == 0 {
		return nil, fmt.Errorf("%w: knowledge_ids are required", agenterrors.ErrInvalidInput)
	}
	var parsed knowledgeSearchInput
	if err := json.Unmarshal(input, &parsed); err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	query := strings.TrimSpace(parsed.Query)
	if query == "" {
		return &ToolResult{ContentText: "query is required", IsError: true}, fmt.Errorf("%w: knowledge search query is required", agenterrors.ErrInvalidInput)
	}
	topK := parsed.TopK
	if topK <= 0 {
		topK = t.DefaultK
	}
	if topK <= 0 {
		topK = 5
	}
	if topK > 20 {
		topK = 20
	}
	mode := retrieval.Mode(strings.TrimSpace(parsed.Mode))
	if mode == "" {
		mode = t.Mode
	}
	if mode == "" {
		mode = retrieval.ModeKeyword
	}
	if mode != retrieval.ModeKeyword && mode != retrieval.ModeVector && mode != retrieval.ModeHybrid {
		return &ToolResult{ContentText: "unsupported retrieval mode", IsError: true}, fmt.Errorf("%w: unsupported retrieval mode", agenterrors.ErrInvalidInput)
	}
	resp, err := t.Retriever.Search(ctx, retrieval.RetrievalRequest{
		OwnerID:         rc.OwnerID,
		KBIDs:           t.KBIDs,
		Query:           query,
		TopK:            topK,
		Mode:            mode,
		EnableHighlight: true,
	})
	if err != nil {
		return &ToolResult{ContentText: err.Error(), IsError: true}, err
	}
	contexts := make([]string, 0, len(resp.Results))
	for _, item := range resp.Results {
		contexts = append(contexts, item.Content)
	}
	output := map[string]any{
		"query":        query,
		"mode":         mode,
		"result_count": len(resp.Results),
		"latency_ms":   resp.LatencyMS,
		"context":      strings.Join(contexts, "\n\n"),
		"results":      resp.Results,
	}
	return ResultFromValue(output)
}
