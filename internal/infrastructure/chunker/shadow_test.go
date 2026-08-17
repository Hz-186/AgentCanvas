package chunker

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"agentcanvas/internal/infrastructure/parser"
)

type shadowTestChunker struct {
	method string
	chunk  []Chunk
	err    error
	calls  int
}

func (c *shadowTestChunker) Method() string { return c.method }

func (c *shadowTestChunker) ChunkDocument(context.Context, parser.ParsedDocument, Policy) ([]Chunk, error) {
	c.calls++
	return c.chunk, c.err
}

func TestShadowChunkerKeepsPrimaryResultWhenCandidateFails(t *testing.T) {
	primary := &shadowTestChunker{method: "recursive", chunk: []Chunk{{Index: 0, Content: "go", TokenCount: 1}}}
	shadow := &shadowTestChunker{method: "python:recursive", err: context.DeadlineExceeded}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	got, err := NewShadowChunker(primary, shadow, logger).ChunkDocument(context.Background(), parser.ParsedDocument{Text: "text"}, Policy{ChunkSize: 8})
	if err != nil || len(got) != 1 || got[0].Content != "go" {
		t.Fatalf("shadow changed primary result: chunks=%+v error=%v", got, err)
	}
	if primary.calls != 1 || shadow.calls != 1 {
		t.Fatalf("unexpected shadow calls: primary=%d shadow=%d", primary.calls, shadow.calls)
	}
}

func TestCompareChunksReportsBoundaryRatio(t *testing.T) {
	page := 2
	metrics := compareChunks(
		[]Chunk{{Content: "same", SectionTitle: "S", PageNo: &page, TokenCount: 2}, {Content: "other", TokenCount: 1}},
		[]Chunk{{Content: "same", SectionTitle: "S", PageNo: &page, TokenCount: 2}},
	)
	if metrics["boundary_match_ratio"] != 0.5 {
		t.Fatalf("boundary ratio = %v, want 0.5", metrics["boundary_match_ratio"])
	}
	if got := overlapCharacters([]Chunk{{Content: "abc中文"}, {Content: "中文def"}}); got != 2 {
		t.Fatalf("overlap characters = %d, want 2", got)
	}
}
