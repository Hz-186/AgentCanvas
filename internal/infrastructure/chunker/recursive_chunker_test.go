package chunker

import (
	"context"
	"strings"
	"testing"

	"agentcanvas/internal/infrastructure/parser"
)

func TestRecursiveChunkerPreservesSectionTitleAndMetadata(t *testing.T) {
	c := NewRecursiveChunker()
	got, err := c.ChunkDocument(context.Background(), parser.ParsedDocument{
		Text: "# Intro\n\n这是第一段。这里还有一句。\n\n这是第二段。",
		Blocks: []parser.DocumentBlock{
			{ID: "b1", Type: "heading", Text: "# Intro"},
			{ID: "b2", Type: "paragraph", Text: "这是第一段。这里还有一句。"},
			{ID: "b3", Type: "paragraph", Text: "这是第二段。"},
		},
	}, Policy{ChunkSize: 100, Overlap: 0})
	if err != nil {
		t.Fatalf("ChunkDocument() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(chunks) = %d, want 1", len(got))
	}
	if got[0].SectionTitle != "Intro" {
		t.Fatalf("SectionTitle = %q, want Intro", got[0].SectionTitle)
	}
	if got[0].Metadata["chunk_method"] != MethodRecursive || got[0].Metadata["tokenizer"] != "estimated" {
		t.Fatalf("metadata = %#v", got[0].Metadata)
	}
}

func TestRecursiveChunkerSplitsOnSentenceBoundaries(t *testing.T) {
	c := NewRecursiveChunker()
	got, err := c.ChunkDocument(context.Background(), parser.ParsedDocument{Text: "第一句很重要。第二句也很重要。第三句继续补充。"}, Policy{ChunkSize: 9, Overlap: 0})
	if err != nil {
		t.Fatalf("ChunkDocument() error = %v", err)
	}
	if len(got) < 2 {
		t.Fatalf("len(chunks) = %d, want split chunks", len(got))
	}
	for _, chunk := range got {
		if chunk.TokenCount > 9 {
			t.Fatalf("TokenCount = %d, want <= 9, content=%q", chunk.TokenCount, chunk.Content)
		}
		if strings.Contains(chunk.Content, "。") && !strings.HasSuffix(chunk.Content, "。") {
			t.Fatalf("chunk should keep sentence boundary: %q", chunk.Content)
		}
	}
}

func TestRecursiveChunkerUsesOverlapWithoutStandaloneOverlapChunk(t *testing.T) {
	c := NewRecursiveChunker()
	text := "一二三四五六七八九十。甲乙丙丁戊己庚辛壬癸。"
	got, err := c.ChunkDocument(context.Background(), parser.ParsedDocument{Text: text}, Policy{ChunkSize: 11, Overlap: 4})
	if err != nil {
		t.Fatalf("ChunkDocument() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(chunks) = %d, want 2: %#v", len(got), got)
	}
	for _, chunk := range got {
		if chunk.TokenCount > 11 {
			t.Fatalf("TokenCount = %d, want <= 11, content=%q", chunk.TokenCount, chunk.Content)
		}
	}
}

func TestRecursiveChunkerKeepsFAQBlocksAsSingleChunks(t *testing.T) {
	c := NewRecursiveChunker()
	got, err := c.ChunkDocument(context.Background(), parser.ParsedDocument{
		Blocks: []parser.DocumentBlock{
			{ID: "faq1", Type: "faq", Text: "What is AgentCanvas?\nAn agent runtime.", Metadata: map[string]any{"chunk_hint": "single_faq", "faq_question": "What is AgentCanvas?", "faq_aliases": []string{"AC"}, "block_type": "faq"}},
			{ID: "faq2", Type: "faq", Text: "Why use RAG?\nGrounded answers.", Metadata: map[string]any{"chunk_hint": "single_faq", "faq_question": "Why use RAG?", "block_type": "faq"}},
		},
	}, Policy{ChunkSize: 1000, Overlap: 0})
	if err != nil {
		t.Fatalf("ChunkDocument() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(chunks) = %d, want one chunk per FAQ: %+v", len(got), got)
	}
	if got[0].Metadata["faq_question"] != "What is AgentCanvas?" || got[0].Metadata["block_type"] != "faq" {
		t.Fatalf("FAQ metadata was not preserved: %+v", got[0].Metadata)
	}
	ids, ok := got[0].Metadata["block_ids"].([]string)
	if !ok || len(ids) != 1 || ids[0] != "faq1" {
		t.Fatalf("expected source block id metadata, got %+v", got[0].Metadata)
	}
}

func TestRecursiveChunkerPreservesPDFPageMetadata(t *testing.T) {
	c := NewRecursiveChunker()
	pageNo := 3
	got, err := c.ChunkDocument(context.Background(), parser.ParsedDocument{
		Blocks: []parser.DocumentBlock{{ID: "p3_b1", Type: "text", Text: "page text", PageNo: &pageNo, Metadata: map[string]any{"page_no": 3, "parser_version": "pdf_text_v1", "block_type": "text"}}},
	}, Policy{ChunkSize: 1000, Overlap: 0})
	if err != nil {
		t.Fatalf("ChunkDocument() error = %v", err)
	}
	if len(got) != 1 || got[0].PageNo == nil || *got[0].PageNo != 3 {
		t.Fatalf("expected page number on chunk, got %+v", got)
	}
	if got[0].Metadata["page_no"] != 3 || got[0].Metadata["parser_version"] != "pdf_text_v1" {
		t.Fatalf("expected PDF metadata on chunk, got %+v", got[0].Metadata)
	}
}
