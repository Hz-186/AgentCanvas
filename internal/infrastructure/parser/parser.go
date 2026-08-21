package parser

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"agentcanvas/internal/pkg/textutil"
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
	faqs := textutil.ParseFAQs(text)
	blocks := make([]DocumentBlock, 0, len(faqs))
	for _, faq := range faqs {
		metadata := map[string]any{"faq_question": faq.Question, "faq_answer": faq.Answer, "faq_aliases": faq.Aliases, "chunk_hint": "single_faq", "block_type": "faq", "parser_version": "faq_v1"}
		if faq.Category != "" {
			metadata["faq_category"] = faq.Category
		}
		blocks = append(blocks, DocumentBlock{ID: fmt.Sprintf("faq%d", len(blocks)+1), Type: "faq", Text: faq.Question + "\n" + faq.Answer, Metadata: metadata})
	}
	return blocks
}

func normalizeFileType(ext string) string {
	if ext == "markdown" {
		return "md"
	}
	return ext
}
