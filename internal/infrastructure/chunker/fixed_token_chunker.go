package chunker

import (
	"strings"
	"unicode/utf8"
)

type Chunk struct {
	Index        int
	Content      string
	TokenCount   int
	CharCount    int
	SectionTitle string
}

type FixedTokenChunker struct{}

func NewFixedTokenChunker() *FixedTokenChunker {
	return &FixedTokenChunker{}
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

	chunks := make([]Chunk, 0, estimateTokens(string(runes))/chunkSize+1)
	start := 0
	for start < len(runes) {
		end := tokenBudgetEnd(runes, start, chunkSize)

		content := strings.TrimSpace(string(runes[start:end]))
		if content != "" {
			chunks = append(chunks, Chunk{
				Index:        len(chunks),
				Content:      content,
				TokenCount:   estimateTokens(content),
				CharCount:    utf8.RuneCountInString(content),
				SectionTitle: inferSectionTitle(content),
			})
		}
		if end == len(runes) {
			break
		}
		nextStart := tokenOverlapStart(runes, start, end, overlap)
		if nextStart <= start {
			nextStart = end
		}
		start = nextStart
	}

	return chunks
}

func tokenBudgetEnd(runes []rune, start, budget int) int {
	end := start
	lastValidEnd := start
	for end < len(runes) {
		candidate := strings.TrimSpace(string(runes[start : end+1]))
		if candidate == "" {
			end++
			continue
		}
		if estimateTokens(candidate) > budget {
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

func tokenOverlapStart(runes []rune, start, end, overlap int) int {
	if overlap <= 0 {
		return end
	}
	for i := end - 1; i >= start; i-- {
		candidate := strings.TrimSpace(string(runes[i:end]))
		if candidate == "" {
			continue
		}
		if estimateTokens(candidate) > overlap {
			return i + 1
		}
	}
	return start
}

func estimateTokens(text string) int {
	runes := []rune(text)
	if len(runes) == 0 {
		return 0
	}
	ascii := 0
	nonASCII := 0
	for _, r := range runes {
		if r <= 127 {
			ascii++
		} else {
			nonASCII++
		}
	}
	estimate := nonASCII + ascii/4
	if ascii%4 != 0 {
		estimate++
	}
	if estimate == 0 {
		return 1
	}
	return estimate
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
