package chunker

import (
	"context"
	"strings"
	"unicode/utf8"

	"agentcanvas/internal/infrastructure/parser"
)

type FixedTokenChunker struct {
	tokenizer Tokenizer
}

func NewFixedTokenChunker(tokenizers ...Tokenizer) *FixedTokenChunker {
	tokenizer := Tokenizer(EstimatedTokenizer{})
	if len(tokenizers) > 0 && tokenizers[0] != nil {
		tokenizer = tokenizers[0]
	}
	return &FixedTokenChunker{tokenizer: tokenizer}
}

func (c *FixedTokenChunker) Method() string { return MethodFixedToken }

func (c *FixedTokenChunker) ChunkDocument(ctx context.Context, doc parser.ParsedDocument, policy Policy) ([]Chunk, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	return c.Chunk(doc.Text, policy.ChunkSize, policy.Overlap), nil
}

func (c *FixedTokenChunker) Chunk(text string, chunkSize, overlap int) []Chunk {
	runes := []rune(strings.TrimSpace(text))
	if len(runes) == 0 {
		return nil
	}
	if chunkSize <= 0 {
		chunkSize = 800
	}
	if overlap < 0 {
		overlap = 0
	}
	if overlap >= chunkSize {
		overlap = chunkSize / 4
	}

	chunks := make([]Chunk, 0, c.countTokens(string(runes))/chunkSize+1)
	start := 0
	for start < len(runes) {
		end := c.tokenBudgetEnd(runes, start, chunkSize)

		content := strings.TrimSpace(string(runes[start:end]))
		if content != "" {
			chunks = append(chunks, Chunk{
				Index:        len(chunks),
				Content:      content,
				TokenCount:   c.countTokens(content),
				CharCount:    utf8.RuneCountInString(content),
				SectionTitle: inferSectionTitle(content),
				Metadata:     map[string]any{"chunk_method": c.Method(), "tokenizer": c.tokenizer.Name()},
			})
		}
		if end == len(runes) {
			break
		}
		nextStart := c.tokenOverlapStart(runes, start, end, overlap)
		if nextStart <= start {
			nextStart = end
		}
		start = nextStart
	}

	return chunks
}

func (c *FixedTokenChunker) tokenBudgetEnd(runes []rune, start, budget int) int {
	end := start
	lastValidEnd := start
	for end < len(runes) {
		candidate := strings.TrimSpace(string(runes[start : end+1]))
		if candidate == "" {
			end++
			continue
		}
		if c.countTokens(candidate) > budget {
			break
		}
		lastValidEnd = end + 1
		end++
	}
	if lastValidEnd == start {
		return start + 1
	}
	return lastValidEnd
}

func (c *FixedTokenChunker) tokenOverlapStart(runes []rune, start, end, overlap int) int {
	if overlap <= 0 {
		return end
	}
	for i := end - 1; i >= start; i-- {
		candidate := strings.TrimSpace(string(runes[i:end]))
		if candidate == "" {
			continue
		}
		if c.countTokens(candidate) > overlap {
			return i + 1
		}
	}
	return start
}

func (c *FixedTokenChunker) countTokens(text string) int {
	if c == nil || c.tokenizer == nil {
		return EstimatedTokenizer{}.Count(text)
	}
	return c.tokenizer.Count(text)
}

func inferSectionTitle(content string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			title := strings.TrimSpace(strings.TrimLeft(line, "#"))
			if title != "" {
				return title
			}
		}
	}
	return ""
}
