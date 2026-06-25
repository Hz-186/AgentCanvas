package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"agentcanvas/internal/domain/retrieval"
)

const maxRerankCandidates = 20

type RerankProviderConfig = ChatProviderConfig

type RerankRequest struct {
	Model   string
	Query   string
	Results []retrieval.RetrievalResult
}

type Reranker interface {
	Rerank(ctx context.Context, cfg RerankProviderConfig, req RerankRequest) ([]retrieval.RetrievalResult, error)
}

type ChatReranker struct {
	ChatClient ChatClient
}

func NewChatReranker(chatClient ChatClient) *ChatReranker {
	return &ChatReranker{ChatClient: chatClient}
}

func (r *ChatReranker) Rerank(ctx context.Context, cfg RerankProviderConfig, req RerankRequest) ([]retrieval.RetrievalResult, error) {
	if r.ChatClient == nil {
		return nil, fmt.Errorf("rerank chat client is not configured")
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		return nil, fmt.Errorf("rerank model is required")
	}
	results := req.Results
	if len(results) > maxRerankCandidates {
		results = results[:maxRerankCandidates]
	}
	items := make([]map[string]any, 0, len(results))
	for _, item := range results {
		content := item.Content
		if len(content) > 800 {
			content = content[:800]
		}
		items = append(items, map[string]any{
			"chunk_id": item.ChunkID,
			"title":    item.DocumentName,
			"content":  content,
		})
	}
	itemsJSON, _ := json.Marshal(items)
	prompt := "You are a retrieval reranker. Return only a JSON array of chunk_id numbers ordered from most relevant to least relevant.\n\nQuery:\n" + req.Query + "\n\nCandidates:\n" + string(itemsJSON)
	temperature := 0.0
	resp, err := r.ChatClient.Chat(ctx, cfg, ChatRequest{
		Model:       model,
		Temperature: &temperature,
		Messages: []ChatMessage{
			{Role: "system", Content: "Return valid JSON only."},
			{Role: "user", Content: prompt},
		},
	})
	if err != nil {
		return nil, err
	}
	var ids []int64
	if err := json.Unmarshal([]byte(strings.TrimSpace(resp.Content)), &ids); err != nil {
		return nil, err
	}
	rank := make(map[int64]int, len(ids))
	for i, id := range ids {
		rank[id] = i
	}
	out := append([]retrieval.RetrievalResult(nil), req.Results...)
	sort.SliceStable(out, func(i, j int) bool {
		ri, okI := rank[out[i].ChunkID]
		rj, okJ := rank[out[j].ChunkID]
		if okI && okJ {
			return ri < rj
		}
		if okI {
			return true
		}
		if okJ {
			return false
		}
		return out[i].FinalScore > out[j].FinalScore
	})
	return out, nil
}
