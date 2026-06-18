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

	chunks := make([]Chunk, 0, len(runes)/chunkSize+1)
	start := 0
	for start < len(runes) {
		end := start + chunkSize
		if end > len(runes) {
			end = len(runes)
		}

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
		start = end - overlap
	}

	return chunks
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
