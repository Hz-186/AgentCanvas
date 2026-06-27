package chunker

import (
	"context"
	"strings"
	"unicode/utf8"

	"agentcanvas/internal/infrastructure/parser"
)

type RecursiveChunker struct {
	tokenizer  Tokenizer
	separators []string
}

func NewRecursiveChunker(tokenizers ...Tokenizer) *RecursiveChunker {
	tokenizer := Tokenizer(EstimatedTokenizer{})
	if len(tokenizers) > 0 && tokenizers[0] != nil {
		tokenizer = tokenizers[0]
	}
	return &RecursiveChunker{
		tokenizer:  tokenizer,
		separators: []string{"\n\n", "\n", "。", "！", "？", ". ", "! ", "? ", "；", ";", "，", ",", " "},
	}
}

func (c *RecursiveChunker) Method() string { return MethodRecursive }

func (c *RecursiveChunker) ChunkDocument(ctx context.Context, doc parser.ParsedDocument, policy Policy) ([]Chunk, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	if policy.ChunkSize <= 0 {
		policy.ChunkSize = 800
	}
	if policy.Overlap < 0 {
		policy.Overlap = 0
	}
	if policy.Overlap >= policy.ChunkSize {
		policy.Overlap = policy.ChunkSize / 4
	}
	segments := c.documentSegments(doc)
	if len(segments) == 0 {
		return nil, nil
	}
	chunks := make([]Chunk, 0, len(segments))
	buffer := segmentBuffer{}
	flush := func() {
		content := strings.TrimSpace(buffer.content)
		if content == "" {
			buffer = segmentBuffer{}
			return
		}
		chunks = append(chunks, Chunk{
			Index:        len(chunks),
			Content:      content,
			TokenCount:   c.count(content),
			CharCount:    utf8.RuneCountInString(content),
			SectionTitle: buffer.sectionTitle,
			PageNo:       buffer.pageNo,
			Metadata: map[string]any{
				"chunk_method": c.Method(),
				"tokenizer":    c.tokenizer.Name(),
				"block_ids":    append([]string(nil), buffer.blockIDs...),
			},
		})
		buffer = c.overlapBuffer(content, policy.Overlap, buffer.sectionTitle, buffer.pageNo)
	}
	for _, segment := range segments {
		pieces := c.splitRecursive(segment.text, c.separators, policy.ChunkSize)
		for _, piece := range pieces {
			piece = strings.TrimSpace(piece)
			if piece == "" {
				continue
			}
			candidate := piece
			if current := strings.TrimSpace(buffer.content); current != "" {
				candidate = current + "\n\n" + piece
			}
			if buffer.content != "" && c.count(candidate) > policy.ChunkSize {
				flush()
				if buffer.content != "" && c.count(buffer.content+"\n\n"+piece) > policy.ChunkSize {
					buffer = segmentBuffer{sectionTitle: buffer.sectionTitle, pageNo: buffer.pageNo}
				}
			}
			if buffer.content != "" {
				buffer.content += "\n\n"
			}
			buffer.content += piece
			if segment.sectionTitle != "" {
				buffer.sectionTitle = segment.sectionTitle
			}
			if buffer.pageNo == nil {
				buffer.pageNo = segment.pageNo
			}
			if segment.blockID != "" {
				buffer.blockIDs = append(buffer.blockIDs, segment.blockID)
			}
		}
	}
	flush()
	for i := range chunks {
		chunks[i].Index = i
	}
	return chunks, nil
}

type chunkSegment struct {
	text         string
	sectionTitle string
	pageNo       *int
	blockID      string
}

type segmentBuffer struct {
	content      string
	sectionTitle string
	pageNo       *int
	blockIDs     []string
}

func (c *RecursiveChunker) documentSegments(doc parser.ParsedDocument) []chunkSegment {
	if len(doc.Blocks) == 0 {
		text := strings.TrimSpace(doc.Text)
		if text == "" {
			return nil
		}
		return []chunkSegment{{text: text, sectionTitle: inferSectionTitle(text)}}
	}
	segments := make([]chunkSegment, 0, len(doc.Blocks))
	sectionTitle := ""
	for _, block := range doc.Blocks {
		text := strings.TrimSpace(block.Text)
		if text == "" {
			continue
		}
		if block.Type == "heading" {
			sectionTitle = strings.TrimSpace(strings.TrimLeft(text, "#"))
			continue
		}
		segments = append(segments, chunkSegment{text: text, sectionTitle: sectionTitle, pageNo: block.PageNo, blockID: block.ID})
	}
	return segments
}

func (c *RecursiveChunker) splitRecursive(text string, separators []string, budget int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	if c.count(text) <= budget {
		return []string{text}
	}
	if len(separators) == 0 {
		return c.splitByRuneBudget(text, budget)
	}
	parts := splitKeepSeparator(text, separators[0])
	if len(parts) <= 1 {
		return c.splitRecursive(text, separators[1:], budget)
	}
	return c.mergePieces(parts, separators[1:], budget)
}

func (c *RecursiveChunker) mergePieces(parts []string, separators []string, budget int) []string {
	merged := make([]string, 0, len(parts))
	buffer := ""
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if c.count(part) > budget {
			if strings.TrimSpace(buffer) != "" {
				merged = append(merged, strings.TrimSpace(buffer))
				buffer = ""
			}
			merged = append(merged, c.splitRecursive(part, separators, budget)...)
			continue
		}
		candidate := part
		if buffer != "" {
			candidate = buffer + " " + part
		}
		if c.count(candidate) > budget {
			merged = append(merged, strings.TrimSpace(buffer))
			buffer = part
		} else {
			buffer = candidate
		}
	}
	if strings.TrimSpace(buffer) != "" {
		merged = append(merged, strings.TrimSpace(buffer))
	}
	return merged
}

func (c *RecursiveChunker) splitByRuneBudget(text string, budget int) []string {
	runes := []rune(text)
	out := make([]string, 0, len(runes)/maxInt(budget, 1)+1)
	start := 0
	for start < len(runes) {
		end := start + 1
		last := end
		for end <= len(runes) {
			if c.count(string(runes[start:end])) > budget {
				break
			}
			last = end
			end++
		}
		out = append(out, strings.TrimSpace(string(runes[start:last])))
		start = last
	}
	return out
}

func (c *RecursiveChunker) overlapBuffer(content string, overlap int, sectionTitle string, pageNo *int) segmentBuffer {
	if overlap <= 0 {
		return segmentBuffer{sectionTitle: sectionTitle, pageNo: pageNo}
	}
	runes := []rune(content)
	start := len(runes)
	for start > 0 && c.count(string(runes[start-1:])) <= overlap {
		start--
	}
	if start < len(runes) {
		return segmentBuffer{content: strings.TrimSpace(string(runes[start:])), sectionTitle: sectionTitle, pageNo: pageNo}
	}
	return segmentBuffer{sectionTitle: sectionTitle, pageNo: pageNo}
}

func (c *RecursiveChunker) count(text string) int {
	if c == nil || c.tokenizer == nil {
		return EstimatedTokenizer{}.Count(text)
	}
	return c.tokenizer.Count(text)
}

func splitKeepSeparator(text, sep string) []string {
	if sep == "" || !strings.Contains(text, sep) {
		return []string{text}
	}
	parts := strings.Split(text, sep)
	out := make([]string, 0, len(parts))
	for i, part := range parts {
		if strings.TrimSpace(part) == "" {
			continue
		}
		if i < len(parts)-1 && sep != "\n\n" && sep != "\n" && sep != " " {
			part += sep
		}
		out = append(out, part)
	}
	return out
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
