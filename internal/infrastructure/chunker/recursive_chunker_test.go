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
