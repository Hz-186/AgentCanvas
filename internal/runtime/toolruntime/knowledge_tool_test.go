package toolruntime

import (
	"context"
	"encoding/json"
	"testing"

	"agentcanvas/internal/domain/retrieval"
)

type fakeRetriever struct {
	req retrieval.RetrievalRequest
}

func (r *fakeRetriever) Search(ctx context.Context, req retrieval.RetrievalRequest) (*retrieval.RetrievalResponse, error) {
	r.req = req
	return &retrieval.RetrievalResponse{
		Results: []retrieval.RetrievalResult{{
			ChunkID: 1,
			KBID:    req.KBIDs[0],
			Content: "agent runtime context",
		}},
		LatencyMS: 7,
	}, nil
}

func TestKnowledgeSearchToolExecutesRetrieval(t *testing.T) {
	retriever := &fakeRetriever{}
	tool := KnowledgeSearchTool{
		Retriever: retriever,
		KBIDs:     []int64{10},
		DefaultK:  4,
		Mode:      retrieval.ModeKeyword,
	}
	result, err := tool.Execute(context.Background(), ToolRunContext{OwnerID: 2}, json.RawMessage(`{"query":"agent loop"}`))
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %+v", result)
	}
	if retriever.req.OwnerID != 2 || retriever.req.Query != "agent loop" || retriever.req.TopK != 4 || retriever.req.Mode != retrieval.ModeKeyword {
		t.Fatalf("unexpected request: %+v", retriever.req)
	}
	var output map[string]any
	if err := json.Unmarshal(result.ContentJSON, &output); err != nil {
		t.Fatal(err)
	}
	if output["result_count"].(float64) != 1 || output["context"] != "agent runtime context" {
		t.Fatalf("unexpected output: %+v", output)
	}
}
