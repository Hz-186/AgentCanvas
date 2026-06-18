package chat_usecase

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"agentcanvas/internal/domain/conversation"
	"agentcanvas/internal/domain/retrieval"
)

const (
	defaultContextBudgetTokens = 6000
	defaultMaxChunksPerDoc     = 3
)

type PackedContext struct {
	Text       string
	References []conversation.MessageReference
}

type ContextPacker struct {
	BudgetTokens    int
	MaxChunksPerDoc int
}

func NewContextPacker() *ContextPacker {
	return &ContextPacker{BudgetTokens: defaultContextBudgetTokens, MaxChunksPerDoc: defaultMaxChunksPerDoc}
}

func (p *ContextPacker) Pack(ownerID int64, results []retrieval.RetrievalResult) PackedContext {
	budget := p.BudgetTokens
	if budget <= 0 {
		budget = defaultContextBudgetTokens
	}
	maxChunks := p.MaxChunksPerDoc
	if maxChunks <= 0 {
		maxChunks = defaultMaxChunksPerDoc
	}

	sorted := append([]retrieval.RetrievalResult(nil), results...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Score > sorted[j].Score })
	perDoc := make(map[int64]int)
	refs := make([]conversation.MessageReference, 0, len(sorted))
	parts := make([]string, 0, len(sorted))
	used := 0

	for _, result := range sorted {
		if perDoc[result.DocumentID] >= maxChunks {
			continue
		}
		content := strings.TrimSpace(result.Content)
		if content == "" {
			continue
		}
		remaining := budget - used
		if remaining <= 0 {
			break
		}
		estimated := estimateTokens(content)
		if estimated > remaining {
			content = truncateByEstimatedTokens(content, remaining)
			estimated = estimateTokens(content)
		}
		if strings.TrimSpace(content) == "" {
			break
		}

		refIndex := len(refs) + 1
		location := fmt.Sprintf("chunk %d", result.ChunkID)
		if result.PageNo != nil {
			location = fmt.Sprintf("页码：%d，chunk %d", *result.PageNo, result.ChunkID)
		}
		parts = append(parts, fmt.Sprintf("[引用 %d] 文档：%s，位置：%s\n内容：%s", refIndex, result.DocumentName, location, content))
		metadataJSON, _ := json.Marshal(result.Metadata)
		refs = append(refs, conversation.MessageReference{
			OwnerID:      ownerID,
			KBID:         result.KBID,
			DocumentID:   result.DocumentID,
			ChunkID:      result.ChunkID,
			RefIndex:     refIndex,
			Score:        result.Score,
			QuoteText:    content,
			PageNo:       result.PageNo,
			MetadataJSON: string(metadataJSON),
		})
		perDoc[result.DocumentID]++
		used += estimated
	}

	return PackedContext{Text: strings.Join(parts, "\n\n"), References: refs}
}

func estimateTokens(text string) int {
	runes := utf8.RuneCountInString(text)
	if runes == 0 {
		return 0
	}
	return (runes + 3) / 4
}

func truncateByEstimatedTokens(text string, maxTokens int) string {
	if maxTokens <= 0 {
		return ""
	}
	maxRunes := maxTokens * 4
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return text
	}
	return string(runes[:maxRunes])
}
