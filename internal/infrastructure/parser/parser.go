package parser

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

type ParsedDocument struct {
	Text     string
	FileType string
	Blocks   []DocumentBlock
}

type DocumentBlock struct {
	ID       string
	Type     string
	Text     string
	PageNo   *int
	BBox     *BBox
	Metadata map[string]any
}

type BBox struct {
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Width  float64 `json:"width"`
	Height float64 `json:"height"`
}

type Parser interface {
	Parse(ctx context.Context, filename string, reader io.Reader) (*ParsedDocument, error)
}

type TextParser struct{}

func NewTextParser() *TextParser {
	return &TextParser{}
}

func (p *TextParser) Parse(ctx context.Context, filename string, reader io.Reader) (*ParsedDocument, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(filename)), ".")
	switch ext {
	case "txt", "md", "markdown":
	default:
		return nil, fmt.Errorf("unsupported file type: %s", ext)
	}

	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, err
	}
	text := strings.TrimPrefix(string(data), "\ufeff")
	return &ParsedDocument{Text: text, FileType: normalizeFileType(ext), Blocks: textBlocks(text)}, nil
}

func textBlocks(text string) []DocumentBlock {
	if blocks := faqBlocks(text); len(blocks) > 0 {
		return blocks
	}
	lines := strings.Split(text, "\n")
	blocks := make([]DocumentBlock, 0, len(lines))
	paragraph := strings.Builder{}
	flushParagraph := func() {
		content := strings.TrimSpace(paragraph.String())
		if content == "" {
			paragraph.Reset()
			return
		}
		blocks = append(blocks, DocumentBlock{ID: fmt.Sprintf("b%d", len(blocks)+1), Type: "paragraph", Text: content})
		paragraph.Reset()
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			flushParagraph()
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			flushParagraph()
			title := strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
			blocks = append(blocks, DocumentBlock{ID: fmt.Sprintf("b%d", len(blocks)+1), Type: "heading", Text: trimmed, Metadata: map[string]any{"title": title}})
			continue
		}
		if paragraph.Len() > 0 {
			paragraph.WriteByte('\n')
		}
		paragraph.WriteString(line)
	}
	flushParagraph()
	return blocks
}

func faqBlocks(text string) []DocumentBlock {
	lines := strings.Split(text, "\n")
	blocks := make([]DocumentBlock, 0)
	for i := 0; i < len(lines); i++ {
		question := strings.TrimSpace(lines[i])
		if !isQuestionLine(question) {
			continue
		}
		answerParts := make([]string, 0, 2)
		aliases := make([]string, 0)
		category := ""
		for j := i + 1; j < len(lines); j++ {
			line := strings.TrimSpace(lines[j])
			if line == "" {
				if len(answerParts) > 0 {
					break
				}
				continue
			}
			if isQuestionLine(line) {
				break
			}
			if values, ok := faqMetadataValues(line, "aliases:", "alias:", "别名:"); ok {
				aliases = append(aliases, values...)
				i = j
				continue
			}
			if values, ok := faqMetadataValues(line, "category:", "分类:"); ok {
				if len(values) > 0 {
					category = values[0]
				}
				i = j
				continue
			}
			answerParts = append(answerParts, strings.TrimPrefix(strings.TrimPrefix(line, "A:"), "答:"))
			i = j
		}
		if len(answerParts) == 0 {
			continue
		}
		canonical := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(question, "Q:"), "问:"))
		answer := strings.TrimSpace(strings.Join(answerParts, "\n"))
		metadata := map[string]any{"faq_question": canonical, "faq_answer": answer, "faq_aliases": aliases, "chunk_hint": "single_faq", "block_type": "faq", "parser_version": "faq_v1"}
		if category != "" {
			metadata["faq_category"] = category
		}
		blocks = append(blocks, DocumentBlock{ID: fmt.Sprintf("faq%d", len(blocks)+1), Type: "faq", Text: canonical + "\n" + answer, Metadata: metadata})
	}
	return blocks
}

func faqMetadataValues(line string, prefixes ...string) ([]string, bool) {
	lower := strings.ToLower(strings.TrimSpace(line))
	for _, prefix := range prefixes {
		if !strings.HasPrefix(lower, prefix) {
			continue
		}
		value := strings.TrimSpace(line[len(prefix):])
		if value == "" {
			return nil, true
		}
		parts := strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == '，' || r == ';' || r == '；' })
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
		return out, true
	}
	return nil, false
}

func isQuestionLine(line string) bool {
	line = strings.TrimSpace(line)
	return strings.HasPrefix(line, "Q:") || strings.HasPrefix(line, "问:") || strings.HasSuffix(line, "?") || strings.HasSuffix(line, "？")
}

func normalizeFileType(ext string) string {
	if ext == "markdown" {
		return "md"
	}
	return ext
}
