package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"agentcanvas/internal/domain/retrieval"
)

type BGEReranker struct {
	Client *http.Client
}

func NewBGEReranker() *BGEReranker {
	return &BGEReranker{Client: &http.Client{Timeout: 30 * time.Second}}
}

func (r *BGEReranker) Rerank(ctx context.Context, cfg RerankProviderConfig, req RerankRequest) ([]retrieval.RetrievalResult, error) {
	if strings.TrimSpace(req.Model) == "" {
		return nil, fmt.Errorf("rerank model is required")
	}
	if len(req.Results) == 0 {
		return nil, fmt.Errorf("rerank results are empty")
	}
	client := r.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	docs := make([]string, 0, len(req.Results))
	for _, item := range req.Results {
		docs = append(docs, item.Content)
	}
	payload := map[string]any{
		"model":     req.Model,
		"query":     req.Query,
		"documents": docs,
		"top_n":     len(docs),
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	endpoint := rerankEndpoint(cfg.BaseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(cfg.APIKey) != "" {
		httpReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(cfg.APIKey))
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("rerank request failed: %s", resp.Status)
	}
	var decoded struct {
		Results []struct {
			Index          int     `json:"index"`
			RelevanceScore float64 `json:"relevance_score"`
			Score          float64 `json:"score"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		return nil, err
	}
	if len(decoded.Results) == 0 {
		return nil, fmt.Errorf("rerank response is empty")
	}
	ranked := append([]retrieval.RetrievalResult(nil), req.Results...)
	scores := make(map[int]float64, len(decoded.Results))
	order := make(map[int]int, len(decoded.Results))
	for i, item := range decoded.Results {
		if item.Index < 0 || item.Index >= len(req.Results) {
			continue
		}
		score := item.RelevanceScore
		if score == 0 {
			score = item.Score
		}
		scores[item.Index] = score
		order[item.Index] = i
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		leftIdx := indexOfResult(req.Results, ranked[i].ChunkID)
		rightIdx := indexOfResult(req.Results, ranked[j].ChunkID)
		leftOrder, leftOK := order[leftIdx]
		rightOrder, rightOK := order[rightIdx]
		if leftOK && rightOK {
			return leftOrder < rightOrder
		}
		if leftOK {
			return true
		}
		if rightOK {
			return false
		}
		return ranked[i].FinalScore > ranked[j].FinalScore
	})
	for i := range ranked {
		idx := indexOfResult(req.Results, ranked[i].ChunkID)
		if score, ok := scores[idx]; ok {
			ranked[i].FinalScore = score
			ranked[i].Score = score
		}
	}
	return ranked, nil
}

func rerankEndpoint(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return "/rerank"
	}
	return baseURL + "/rerank"
}

func indexOfResult(items []retrieval.RetrievalResult, chunkID int64) int {
	for i, item := range items {
		if item.ChunkID == chunkID {
			return i
		}
	}
	return -1
}
